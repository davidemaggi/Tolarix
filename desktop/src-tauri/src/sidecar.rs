//! Starts and stops the bundled `tolarix` server (the sidecar).
//!
//! The server listens on 127.0.0.1 on a port the OS picks, prints
//! `TOLARIX_READY {"port":…}` on stdout once it accepts connections, and
//! accepts only requests that carry the per-launch token. It shuts down
//! cleanly when its stdin closes, which also happens if this app crashes.

use std::fs::{self, File, OpenOptions};
use std::io::{BufRead, BufReader, Read, Seek, SeekFrom};
use std::path::{Path, PathBuf};
use std::process::{Child, ChildStdin, Command, Stdio};
use std::sync::mpsc;
use std::thread;
use std::time::{Duration, Instant};

use url::Url;

/// Printed by `tolarix serve` once it listens.
pub const READY_PREFIX: &str = "TOLARIX_READY ";
/// How long the server may take to start (first launch migrates the database).
pub const READY_TIMEOUT: Duration = Duration::from_secs(30);
/// How long a clean shutdown may take before the server is killed.
pub const STOP_TIMEOUT: Duration = Duration::from_secs(10);
/// The log is moved to `<name>.1` when it grows past this size.
const LOG_LIMIT: u64 = 5 * 1024 * 1024;

/// A random launch token: 32 bytes, hex encoded.
pub fn new_token() -> Result<String, String> {
    let mut b = [0u8; 32];
    getrandom::fill(&mut b).map_err(|e| format!("cannot generate a launch token: {e}"))?;
    Ok(b.iter().map(|x| format!("{x:02x}")).collect())
}

/// The port announced by a ready line, if `line` is one.
pub fn parse_ready(line: &str) -> Option<u16> {
    let json = line.trim_end().strip_prefix(READY_PREFIX)?;
    let v: serde_json::Value = serde_json::from_str(json).ok()?;
    let port = v.get("port")?.as_u64()?;
    u16::try_from(port).ok().filter(|p| *p != 0)
}

/// The URL that opens the UI and trades the token for a session cookie.
pub fn launch_url(port: u16, token: &str) -> Url {
    let mut u = Url::parse(&format!("http://127.0.0.1:{port}/")).expect("valid URL");
    u.query_pairs_mut().append_pair("token", token);
    u
}

/// Whether the main window may show `url`: the bundled splash page or the
/// local server. Everything else opens in the system browser.
pub fn is_internal(url: &Url, port: u16) -> bool {
    match url.scheme() {
        // The splash page: tauri://localhost (macOS, Linux) or
        // http://tauri.localhost (Windows).
        "tauri" => true,
        "about" => url.as_str() == "about:blank",
        "http" => match url.host_str() {
            Some("tauri.localhost") => true,
            Some("127.0.0.1") => port != 0 && url.port() == Some(port),
            _ => false,
        },
        _ => false,
    }
}

/// Whether `url` may be handed to the system (browser or mail client).
pub fn is_openable(url: &Url) -> bool {
    matches!(url.scheme(), "http" | "https" | "mailto")
}

/// The sidecar sits next to the app executable (Tauri's `externalBin`).
pub fn binary_path() -> Result<PathBuf, String> {
    let exe = std::env::current_exe().map_err(|e| format!("cannot locate the app: {e}"))?;
    let dir = exe.parent().ok_or("cannot locate the app folder")?;
    Ok(dir.join(format!("tolarix{}", std::env::consts::EXE_SUFFIX)))
}

/// Opens the server log for appending, moving a large one aside first.
fn open_log(path: &Path) -> std::io::Result<File> {
    if let Some(dir) = path.parent() {
        fs::create_dir_all(dir)?;
    }
    if fs::metadata(path)
        .map(|m| m.len() > LOG_LIMIT)
        .unwrap_or(false)
    {
        let _ = fs::rename(path, path.with_extension("log.1"));
    }
    OpenOptions::new().create(true).append(true).open(path)
}

/// The last `n` lines of the log written since `from`, to explain a failure.
fn log_tail(path: &Path, from: u64, n: usize) -> String {
    let mut s = String::new();
    if let Ok(mut f) = File::open(path) {
        let _ = f.seek(SeekFrom::Start(from));
        let _ = f.take(256 * 1024).read_to_string(&mut s);
    }
    let lines: Vec<&str> = s.lines().filter(|l| !l.trim().is_empty()).collect();
    lines[lines.len().saturating_sub(n)..].join("\n")
}

/// A running server. Dropping stdin asks it to stop.
pub struct Sidecar {
    child: Child,
    stdin: Option<ChildStdin>,
}

impl Sidecar {
    /// Starts the server and waits for its ready line. Returns the port.
    pub fn start(bin: &Path, token: &str, log: &Path) -> Result<(Sidecar, u16), String> {
        let log_file = open_log(log).map_err(|e| format!("cannot open {}: {e}", log.display()))?;
        let log_start = log_file.metadata().map(|m| m.len()).unwrap_or(0);
        let stderr = log_file
            .try_clone()
            .map_err(|e| format!("cannot open {}: {e}", log.display()))?;

        let mut cmd = Command::new(bin);
        cmd.args(["serve", "--mode", "desktop", "--stop-on-stdin-close"])
            // The token goes through the environment: arguments are visible
            // to other users of the machine.
            .env("TOLARIX_DESKTOP_TOKEN", token)
            // A new secret key goes to the OS keychain (file fallback).
            .env("TOLARIX_SECRET_KEY_STORE", "keychain")
            .env_remove("TOLARIX_PORT")
            .env_remove("TOLARIX_HOST")
            .env_remove("TOLARIX_MODE")
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::from(stderr));
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            const CREATE_NO_WINDOW: u32 = 0x0800_0000;
            cmd.creation_flags(CREATE_NO_WINDOW);
        }
        let mut child = cmd
            .spawn()
            .map_err(|e| format!("cannot start {}: {e}", bin.display()))?;
        let stdin = child.stdin.take();
        let stdout = child.stdout.take().expect("stdout is piped");

        let (tx, rx) = mpsc::channel();
        thread::spawn(move || {
            let mut tx = Some(tx);
            // Keep draining stdout after the ready line so the server never
            // blocks on a full pipe.
            for line in BufReader::new(stdout).lines() {
                let Ok(line) = line else { break };
                if let Some(port) = parse_ready(&line)
                    && let Some(tx) = tx.take()
                {
                    let _ = tx.send(port);
                }
            }
        });

        let mut sc = Sidecar { child, stdin };
        let deadline = Instant::now() + READY_TIMEOUT;
        loop {
            match rx.recv_timeout(Duration::from_millis(200)) {
                Ok(port) => return Ok((sc, port)),
                Err(mpsc::RecvTimeoutError::Disconnected) => {
                    // stdout closed without a ready line: the server exited.
                    let _ = sc.child.wait();
                    return Err(failure(
                        "The Tolarix server stopped while starting.",
                        log,
                        log_start,
                    ));
                }
                Err(mpsc::RecvTimeoutError::Timeout) => {
                    if let Ok(Some(_)) = sc.child.try_wait() {
                        return Err(failure(
                            "The Tolarix server stopped while starting.",
                            log,
                            log_start,
                        ));
                    }
                    if Instant::now() >= deadline {
                        sc.stop();
                        return Err(failure(
                            "The Tolarix server did not start in time.",
                            log,
                            log_start,
                        ));
                    }
                }
            }
        }
    }

    /// Asks the server to stop, waits for it, and kills it if it hangs.
    pub fn stop(&mut self) {
        drop(self.stdin.take());
        let deadline = Instant::now() + STOP_TIMEOUT;
        while Instant::now() < deadline {
            match self.child.try_wait() {
                Ok(Some(_)) | Err(_) => return,
                Ok(None) => thread::sleep(Duration::from_millis(50)),
            }
        }
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

fn failure(msg: &str, log: &Path, from: u64) -> String {
    let tail = log_tail(log, from, 12);
    if tail.is_empty() {
        format!("{msg}\n\nLog: {}", log.display())
    } else {
        format!("{msg}\n\n{tail}\n\nLog: {}", log.display())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_the_ready_line() {
        assert_eq!(
            parse_ready(r#"TOLARIX_READY {"port":49152,"mode":"desktop"}"#),
            Some(49152)
        );
        assert_eq!(parse_ready("TOLARIX_READY {\"port\":8080}\r\n"), Some(8080));
        assert_eq!(parse_ready(r#"TOLARIX_READY {"port":0}"#), None);
        assert_eq!(parse_ready(r#"TOLARIX_READY {"port":70000}"#), None);
        assert_eq!(parse_ready(r#"TOLARIX_READY {"port":"80"}"#), None);
        assert_eq!(parse_ready("TOLARIX_READY not json"), None);
        assert_eq!(parse_ready(r#"listening {"port":8080}"#), None);
    }

    #[test]
    fn tokens_are_random_hex() {
        let a = new_token().unwrap();
        let b = new_token().unwrap();
        assert_eq!(a.len(), 64);
        assert!(a.chars().all(|c| c.is_ascii_hexdigit()));
        assert_ne!(a, b);
    }

    #[test]
    fn launch_url_carries_the_token() {
        assert_eq!(
            launch_url(4321, "abc").as_str(),
            "http://127.0.0.1:4321/?token=abc"
        );
    }

    #[test]
    fn only_the_splash_and_the_local_server_are_internal() {
        let u = |s: &str| Url::parse(s).unwrap();
        assert!(is_internal(&u("tauri://localhost/index.html"), 0));
        assert!(is_internal(&u("http://tauri.localhost/index.html"), 0));
        assert!(is_internal(&u("http://127.0.0.1:4321/flows"), 4321));
        assert!(!is_internal(&u("http://127.0.0.1:4321/flows"), 0));
        assert!(!is_internal(&u("http://127.0.0.1:9999/"), 4321));
        assert!(!is_internal(&u("http://localhost:4321/"), 4321));
        assert!(!is_internal(&u("https://127.0.0.1:4321/"), 4321));
        assert!(!is_internal(&u("https://example.com/"), 4321));
        assert!(!is_internal(&u("file:///etc/passwd"), 4321));
    }

    #[test]
    fn only_web_and_mail_links_open_outside() {
        let u = |s: &str| Url::parse(s).unwrap();
        assert!(is_openable(&u("https://dev.azure.com/org")));
        assert!(is_openable(&u("mailto:someone@example.com")));
        assert!(!is_openable(&u("file:///etc/passwd")));
        assert!(!is_openable(&u("smb://server/share")));
    }

    #[test]
    fn the_log_tail_skips_earlier_runs() {
        let dir = std::env::temp_dir().join(format!("tolarix-test-{}", std::process::id()));
        let log = dir.join("server.log");
        let _ = fs::remove_dir_all(&dir);
        {
            use std::io::Write;
            let mut f = open_log(&log).unwrap();
            writeln!(f, "old run").unwrap();
        }
        let from = fs::metadata(&log).unwrap().len();
        {
            use std::io::Write;
            let mut f = open_log(&log).unwrap();
            writeln!(f, "line 1\n\nline 2\nError: boom").unwrap();
        }
        assert_eq!(log_tail(&log, from, 2), "line 2\nError: boom");
        assert_eq!(log_tail(&log, from, 10), "line 1\nline 2\nError: boom");
        let _ = fs::remove_dir_all(&dir);
    }

    #[cfg(unix)]
    #[test]
    fn reports_a_server_that_exits_while_starting() {
        let dir = std::env::temp_dir().join(format!("tolarix-exit-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        fs::create_dir_all(&dir).unwrap();
        let bin = dir.join("fake-tolarix");
        fs::write(
            &bin,
            "#!/bin/sh\necho 'Error: database is locked' >&2\nexit 3\n",
        )
        .unwrap();
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(&bin, fs::Permissions::from_mode(0o755)).unwrap();
        let err = match Sidecar::start(&bin, "t", &dir.join("server.log")) {
            Ok(_) => panic!("expected an error"),
            Err(e) => e,
        };
        assert!(err.contains("stopped while starting"), "{err}");
        assert!(err.contains("database is locked"), "{err}");
        let _ = fs::remove_dir_all(&dir);
    }

    #[cfg(unix)]
    #[test]
    fn starts_and_stops_a_server() {
        let dir = std::env::temp_dir().join(format!("tolarix-run-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        fs::create_dir_all(&dir).unwrap();
        let bin = dir.join("fake-tolarix");
        // Prints the ready line, then exits once stdin closes.
        fs::write(
            &bin,
            "#!/bin/sh\n[ -n \"$TOLARIX_DESKTOP_TOKEN\" ] || exit 9\necho 'TOLARIX_READY {\"port\":4321}'\ncat >/dev/null\n",
        )
        .unwrap();
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(&bin, fs::Permissions::from_mode(0o755)).unwrap();
        let (mut sc, port) = Sidecar::start(&bin, "token", &dir.join("server.log")).unwrap();
        assert_eq!(port, 4321);
        let t = Instant::now();
        sc.stop();
        assert!(t.elapsed() < STOP_TIMEOUT, "stopped by closing stdin");
        let _ = fs::remove_dir_all(&dir);
    }
}
