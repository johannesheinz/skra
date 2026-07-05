// Progressive enhancement for the contact-list preference controls.
//
// Without JS, the controls are a normal form with an Apply button. With JS, the
// <select>s auto-submit on change (so a choice applies and persists immediately)
// and the now-redundant Apply button is hidden. The same enhancement re-runs on
// htmx-swapped content (e.g. live search replacing the results region), so the
// controls it brings in are enhanced too rather than reverting to the no-JS form.
function enhancePrefs(root) {
  root.querySelectorAll("[data-js-hide]").forEach(function (el) {
    el.hidden = true;
  });
  root.querySelectorAll("[data-autosubmit] [data-autosubmit-control]").forEach(function (el) {
    if (el.dataset.autosubmitWired) return;
    el.dataset.autosubmitWired = "1";
    el.addEventListener("change", function () {
      var form = el.form || el.closest("form");
      if (!form) return;
      if (form.requestSubmit) form.requestSubmit();
      else form.submit();
    });
  });
}

document.addEventListener("DOMContentLoaded", function () {
  enhancePrefs(document);
  document.body.addEventListener("htmx:afterSwap", function (e) {
    enhancePrefs(e.target);
  });
});
