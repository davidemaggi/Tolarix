//! Tolarix desktop shell: starts the bundled server and shows its UI.

mod sidecar;

use std::sync::atomic::{AtomicU16, Ordering};
use std::sync::{Arc, Mutex};

use tauri::webview::NewWindowResponse;
use tauri::{AppHandle, Manager, RunEvent, WebviewUrl, WebviewWindow, WebviewWindowBuilder};
use tauri_plugin_opener::OpenerExt;

use sidecar::Sidecar;

/// The running server, stopped when the app exits.
#[derive(Default)]
struct Server(Mutex<Option<Sidecar>>);

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    // 0 until the server is ready: only the splash page may load before.
    let port = Arc::new(AtomicU16::new(0));

    let app = tauri::Builder::default()
        // A second launch focuses this window: two servers (and two
        // schedulers) on one database would step on each other.
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            if let Some(w) = app.get_webview_window("main") {
                let _ = w.unminimize();
                let _ = w.show();
                let _ = w.set_focus();
            }
        }))
        .plugin(tauri_plugin_opener::init())
        .manage(Server::default())
        .setup(move |app| {
            let window = main_window(app.handle(), port.clone())?;
            let handle = app.handle().clone();
            std::thread::spawn(move || start_server(handle, window, port));
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building the Tolarix app");

    app.run(|app, event| {
        if let RunEvent::Exit = event
            && let Some(mut s) = app.state::<Server>().0.lock().unwrap().take()
        {
            s.stop();
        }
    });
}

fn main_window(app: &AppHandle, port: Arc<AtomicU16>) -> tauri::Result<WebviewWindow> {
    let nav_app = app.clone();
    let nav_port = port.clone();
    let popup_app = app.clone();
    WebviewWindowBuilder::new(app, "main", WebviewUrl::App("index.html".into()))
        .title("Tolarix")
        .inner_size(1280.0, 840.0)
        .min_inner_size(900.0, 600.0)
        .on_navigation(move |url| {
            if sidecar::is_internal(url, nav_port.load(Ordering::SeqCst)) {
                return true;
            }
            open_outside(&nav_app, url);
            false
        })
        // Links with target="_blank" and window.open: the UI has a single
        // window, so external pages go to the system browser.
        .on_new_window(move |url, _| {
            if !sidecar::is_internal(&url, port.load(Ordering::SeqCst)) {
                open_outside(&popup_app, &url);
            }
            NewWindowResponse::Deny
        })
        .build()
}

fn open_outside(app: &AppHandle, url: &url::Url) {
    if sidecar::is_openable(url) {
        let _ = app.opener().open_url(url.as_str(), None::<&str>);
    }
}

fn start_server(app: AppHandle, window: WebviewWindow, port: Arc<AtomicU16>) {
    let started = (|| {
        let token = sidecar::new_token()?;
        let bin = sidecar::binary_path()?;
        let log = app
            .path()
            .app_log_dir()
            .map_err(|e| format!("cannot locate the log folder: {e}"))?
            .join("server.log");
        let (server, p) = Sidecar::start(&bin, &token, &log)?;
        Ok::<_, String>((server, p, token))
    })();

    match started {
        Ok((server, p, token)) => {
            *app.state::<Server>().0.lock().unwrap() = Some(server);
            port.store(p, Ordering::SeqCst);
            if let Err(e) = window.navigate(sidecar::launch_url(p, &token)) {
                show_error(&window, &format!("Cannot open the Tolarix UI: {e}"));
            }
        }
        Err(e) => show_error(&window, &e),
    }
}

/// Shows a startup failure on the splash page. The server can fail before
/// the splash has loaded, so the (idempotent) call is repeated for a while.
fn show_error(window: &WebviewWindow, msg: &str) {
    let arg = serde_json::to_string(msg).unwrap_or_else(|_| "\"\"".into());
    let js = format!("window.showError && window.showError({arg})");
    for _ in 0..10 {
        let _ = window.eval(&js);
        std::thread::sleep(std::time::Duration::from_millis(300));
    }
}
