// Progressive enhancement for the contact-list preference controls.
//
// Without JS, the controls are a normal form with an Apply button. With JS, the
// <select>s auto-submit on change (so a choice applies and persists immediately)
// and the now-redundant Apply button is hidden.
document.addEventListener("DOMContentLoaded", function () {
  document.querySelectorAll("[data-js-hide]").forEach(function (el) {
    el.hidden = true;
  });
  document.querySelectorAll("[data-autosubmit] [data-autosubmit-control]").forEach(function (el) {
    el.addEventListener("change", function () {
      var form = el.form || el.closest("form");
      if (!form) return;
      if (form.requestSubmit) form.requestSubmit();
      else form.submit();
    });
  });
});
