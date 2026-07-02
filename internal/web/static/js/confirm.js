// Ask for confirmation before a destructive form submits. A form opts in with
// data-confirm="<localized prompt>"; without JS the form just submits (the
// server still requires the POST + CSRF, so nothing is destroyed by a stray
// GET). Capture phase so this runs before any other submit handler.
document.addEventListener(
  "submit",
  function (e) {
    var form = e.target;
    if (!form || !form.getAttribute) return;
    var msg = form.getAttribute("data-confirm");
    if (!msg) return;
    if (!window.confirm(msg)) {
      e.preventDefault();
      e.stopPropagation();
    }
  },
  true,
);
