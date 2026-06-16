// Light/dark theme preference. Loaded synchronously in <head> so the stored
// choice is applied before first paint (no flash). No inline script, so the
// self-only CSP stays intact.
(function () {
  try {
    var stored = localStorage.getItem("skra-theme");
    if (stored === "dark" || stored === "light") {
      document.documentElement.setAttribute("data-theme", stored);
    }
  } catch (e) {
    /* localStorage unavailable — fall back to prefers-color-scheme */
  }
})();

document.addEventListener("DOMContentLoaded", function () {
  var toggle = document.querySelector("[data-theme-toggle]");
  if (!toggle) return;
  toggle.addEventListener("click", function () {
    var current = document.documentElement.getAttribute("data-theme");
    var next;
    if (current === "dark") {
      next = "light";
    } else if (current === "light") {
      next = "dark";
    } else {
      // No explicit choice yet: flip away from the OS setting.
      next = window.matchMedia("(prefers-color-scheme: dark)").matches ? "light" : "dark";
    }
    document.documentElement.setAttribute("data-theme", next);
    try {
      localStorage.setItem("skra-theme", next);
    } catch (e) {
      /* ignore */
    }
  });
});
