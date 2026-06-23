// Birthday "no year" toggle. When checked, the full date picker is replaced by
// month/day selects so no year can be entered; unchecking brings the year back.
// The server reads whichever set is active based on the checkbox, so this is
// purely presentational (and degrades to both sets being visible without JS).
document.addEventListener("DOMContentLoaded", function () {
  document.querySelectorAll("[data-birthday-noyear]").forEach(function (cb) {
    var scope = cb.closest("form") || document;
    var dateWrap = scope.querySelector("[data-birthday-date]");
    var mdWrap = scope.querySelector("[data-birthday-md]");
    if (!dateWrap || !mdWrap) return;
    function apply() {
      dateWrap.hidden = cb.checked;
      mdWrap.hidden = !cb.checked;
    }
    cb.addEventListener("change", apply);
    apply();
  });
});
