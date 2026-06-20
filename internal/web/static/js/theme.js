// Theme preferences. Loaded synchronously in <head> so a stored choice applies
// before first paint (no flash). No inline script, so the self-only CSP stays
// intact.
//
// Precedence: localStorage is the device's effective theme and overrides the
// server-rendered attributes (which come from the account's saved preferences
// and act as the cross-device default / seed for a new device).
(function () {
  var el = document.documentElement;
  var map = { "skra-theme": "data-theme", "skra-flavor": "data-flavor", "skra-accent": "data-accent" };
  try {
    for (var key in map) {
      var v = localStorage.getItem(key);
      if (v) el.setAttribute(map[key], v);
      else if (v === "") el.removeAttribute(map[key]); // explicit "system"/default
    }
  } catch (e) {
    /* localStorage unavailable — fall back to server attributes / prefers-color-scheme */
  }
})();

document.addEventListener("DOMContentLoaded", function () {
  var el = document.documentElement;

  function store(key, value) {
    try {
      localStorage.setItem(key, value);
    } catch (e) {
      /* ignore */
    }
  }

  // Header quick-toggle: flip light/dark on this device.
  var toggle = document.querySelector("[data-theme-toggle]");
  if (toggle) {
    toggle.addEventListener("click", function () {
      var current = el.getAttribute("data-theme");
      var next;
      if (current === "dark") next = "light";
      else if (current === "light") next = "dark";
      else next = window.matchMedia("(prefers-color-scheme: dark)").matches ? "light" : "dark";
      el.setAttribute("data-theme", next);
      store("skra-theme", next);
    });
  }

  // Appearance form: mirror the saved selection into localStorage so this device
  // reflects the change immediately (the form also persists it to the account).
  var form = document.querySelector("[data-theme-form]");
  if (form) {
    form.addEventListener("submit", function () {
      store("skra-theme", form.elements["mode"].value);
      store("skra-flavor", form.elements["flavor"].value);
      var accent = form.querySelector('input[name="accent"]:checked');
      store("skra-accent", accent ? accent.value : "");
    });
  }
});
