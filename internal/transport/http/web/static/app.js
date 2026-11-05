// htmx action errors: the panel sends them in the HX-Trigger header: {"showError": "..."}.
document.body.addEventListener("showError", function (e) { alert(e.detail.value); });
// The "Copy" button next to the subscription link.
document.body.addEventListener("click", function (e) {
  var btn = e.target.closest("[data-copy]");
  if (!btn) return;
  var el = document.getElementById(btn.dataset.copy);
  navigator.clipboard.writeText(el.value || el.textContent).then(function () {
    var old = btn.textContent; btn.textContent = "Copied"; setTimeout(function () { btn.textContent = old; }, 1500);
  });
});
