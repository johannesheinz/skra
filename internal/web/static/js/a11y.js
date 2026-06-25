// Accessibility enhancements for the forms. Loaded with defer, so the DOM (and
// body) already exist when this runs.
(function () {
  function announce(kind) {
    var live = document.querySelector("[data-live-region]");
    if (live) live.textContent = live.getAttribute("data-" + kind) || "";
  }

  var REPEATABLE = ["emails", "phones", "addresses", "urls"];

  // Remove a repeatable field row (delegated so htmx-added rows work too).
  document.addEventListener("click", function (e) {
    var btn = e.target.closest("[data-remove-row]");
    if (!btn) return;
    var row = btn.closest(".field-row");
    if (row) {
      row.remove();
      announce("removed");
    }
  });

  // When htmx appends a row, move focus to its first field and announce it.
  if (document.body) {
    document.body.addEventListener("htmx:afterSwap", function (e) {
      var c = e.target;
      if (!c || REPEATABLE.indexOf(c.id) === -1) return;
      var last = c.lastElementChild;
      var field = last && last.querySelector("input, select");
      if (field) field.focus();
      announce("added");
    });
  }

  // Move focus to a validation alert so it is announced and reachable.
  var alert = document.querySelector('[role="alert"]');
  if (alert) {
    alert.setAttribute("tabindex", "-1");
    alert.focus();
  }
})();
