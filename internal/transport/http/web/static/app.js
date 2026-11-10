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

// A restored history snapshot keeps stale component state: reinitialize it.
document.body.addEventListener("htmx:historyRestore", function () { window.basecoat.initAll({ force: true }); });

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
