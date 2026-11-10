// CSP forbids inline handlers, so every interactive element is wired here by data attributes.
function toast(category, title) {
  var toaster = document.getElementById("toaster");
  if (toaster && toaster.toast) toaster.toast({ category: category, title: title });
  else alert(title);
}

// htmx action errors: the panel sends them in the HX-Trigger header: {"showError": "..."}.
document.body.addEventListener("showError", function (e) { toast("error", e.detail.value); });

document.body.addEventListener("click", function (e) {
  var el;
  if ((el = e.target.closest("[data-copy]"))) {
    var src = document.getElementById(el.dataset.copy);
    navigator.clipboard.writeText(src.value || src.textContent).then(function () { toast("success", "Copied"); });
  } else if ((el = e.target.closest("[data-dialog-open]"))) {
    document.getElementById(el.dataset.dialogOpen).showModal();
  } else if ((el = e.target.closest("[data-dialog-close]"))) {
    el.closest("dialog").close();
  } else if (e.target.matches("dialog.dialog")) {
    e.target.close(); // a click on the backdrop
  } else if ((el = e.target.closest("[data-sidebar-toggle]"))) {
    var sidebar = document.querySelector(".sidebar");
    if (sidebar && sidebar.toggle) sidebar.toggle();
  } else if ((el = e.target.closest("[data-theme-toggle]"))) {
    window.basecoat.theme.toggle();
  }
});

// Charts take their data from data-chart-* attributes; Chart.js is loaded only on pages that have them.
function initCharts() {
  if (!window.basecoat || !window.basecoat.chart) return;
  // Thin out day labels instead of tilting them; scale defaults merge with Basecoat's tick colors.
  window.Chart.defaults.scale.ticks.maxRotation = 0;
  window.Chart.defaults.scale.ticks.autoSkipPadding = 16;
  document.querySelectorAll("canvas[data-chart-type]").forEach(function (canvas) {
    var stacked = canvas.dataset.chartStacked === "true";
    window.basecoat.chart(canvas, { options: { scales: { x: { stacked: stacked }, y: { stacked: stacked } } } });
  });
}
document.addEventListener("DOMContentLoaded", initCharts);

// A restored history snapshot keeps stale component state: reinitialize it.
document.body.addEventListener("htmx:historyRestore", function () {
  window.basecoat.initAll({ force: true });
  initCharts();
});

// Forms inside a tab post to an action with #<tab>; the browser keeps the fragment across the redirect
// back to the page, so the same tab opens again. Basecoat initializes tabs on DOMContentLoaded first.
document.addEventListener("DOMContentLoaded", function () {
  var tab = location.hash && document.getElementById("tab-" + location.hash.slice(1));
  if (tab && tab.getAttribute("role") === "tab") tab.click();
});
document.body.addEventListener("click", function (e) {
  var tab = e.target.closest('[role="tab"][id^="tab-"]');
  if (tab) history.replaceState(null, "", "#" + tab.id.slice(4));
});
