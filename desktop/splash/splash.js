// Shown while the bundled server starts. The app calls showError() if it
// cannot start. The UI language follows the system, like the web UI.
(function () {
  var it = /^it\b/i.test(navigator.language || "");
  var text = it
    ? {
        starting: "Avvio in corso…",
        title: "Impossibile avviare Tolarix",
        hint: "Chiudi l'app e riaprila. Se il problema persiste, controlla il log.",
      }
    : {
        starting: "Starting…",
        title: "Tolarix could not start",
        hint: "Close the app and open it again. If the problem persists, check the log.",
      };
  var $ = function (id) {
    return document.getElementById(id);
  };
  document.documentElement.lang = it ? "it" : "en";
  $("status").textContent = text.starting;
  $("error-title").textContent = text.title;
  $("error-hint").textContent = text.hint;

  window.showError = function (msg) {
    $("status").hidden = true;
    $("error-detail").textContent = String(msg);
    $("error").hidden = false;
  };
})();
