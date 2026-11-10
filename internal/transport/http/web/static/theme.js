// Loaded synchronously in <head> so the saved theme is applied before the first paint.
(function () {
  var mode = null;
  try { mode = localStorage.getItem("themeMode"); } catch (_) {}
  var dark = mode ? mode === "dark" : window.matchMedia("(prefers-color-scheme: dark)").matches;
  document.documentElement.classList.toggle("dark", dark);
})();
