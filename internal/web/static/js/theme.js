// Theme preferences.
//
// Logged-in pages are server-managed: the <html data-theme-managed> attribute is
// present and the theme attributes are rendered from the account's saved
// preferences (authoritative, no flash). This script then does nothing, so the
// header control (a form that persists to the account) and the Appearance
// settings never disagree.
//
// Logged-out pages (login, gated shares) have no account, so the theme is kept
// per-device in localStorage and applied here before first paint.
(function () {
  var el = document.documentElement;
  if (el.hasAttribute("data-theme-managed")) return;
  var map = { "skra-theme": "data-theme", "skra-flavor": "data-flavor", "skra-accent": "data-accent" };
  try {
    for (var key in map) {
      var v = localStorage.getItem(key);
      if (v) el.setAttribute(map[key], v);
    }
  } catch (e) {
    /* localStorage unavailable — fall back to prefers-color-scheme */
  }
})();

document.addEventListener("DOMContentLoaded", function () {
  // Only present when logged out; logged-in uses the persisting form instead.
  var toggle = document.querySelector("[data-theme-toggle]");
  if (!toggle) return;
  toggle.addEventListener("click", function () {
    var el = document.documentElement;
    var current = el.getAttribute("data-theme");
    var next;
    if (current === "dark") next = "light";
    else if (current === "light") next = "dark";
    else next = window.matchMedia("(prefers-color-scheme: dark)").matches ? "light" : "dark";
    el.setAttribute("data-theme", next);
    try {
      localStorage.setItem("skra-theme", next);
    } catch (e) {
      /* ignore */
    }
  });
});
