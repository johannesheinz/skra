// Remove a repeatable field row (email/phone/address/link) on click. Uses event
// delegation on the document so it also handles rows added later via htmx.
document.addEventListener("click", function (e) {
  var btn = e.target.closest("[data-remove-row]");
  if (!btn) return;
  var row = btn.closest(".field-row");
  if (row) row.remove();
});
