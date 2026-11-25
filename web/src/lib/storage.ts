// localStorage can be unavailable (private windows, blocked site data, the
// desktop webview in some states). These helpers never throw.

export function readStorage(key: string): string | null {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

export function writeStorage(key: string, value: string): void {
  try {
    window.localStorage.setItem(key, value);
  } catch {
    // Preference just won't persist.
  }
}
