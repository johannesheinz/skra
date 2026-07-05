// Light-dismiss for <details class="menu"> dropdowns: close an open menu when a
// click lands outside it or Escape is pressed. Without JS the menu still works
// as a native disclosure (click the summary to toggle); this only adds the
// expected "click away to close" behaviour.
document.addEventListener("click", function (e) {
  document.querySelectorAll("details.menu[open]").forEach(function (d) {
    if (!d.contains(e.target)) d.removeAttribute("open");
  });
});
document.addEventListener("keydown", function (e) {
  if (e.key !== "Escape") return;
  document.querySelectorAll("details.menu[open]").forEach(function (d) {
    d.removeAttribute("open");
    var summary = d.querySelector("summary");
    if (summary) summary.focus();
  });
});
