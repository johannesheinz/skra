// Progressive enhancement for the contact photo control: preview the chosen
// image before saving. Works without JS (the Save button just uploads); with
// JS, picking a file shows a live preview and enables Save. Uses a data: URL
// (FileReader) so it complies with the img-src 'self' data: CSP.
document.addEventListener("DOMContentLoaded", function () {
  var input = document.querySelector("[data-photo-input]");
  if (!input) return;
  var form = input.closest("[data-photo-form]");
  var save = form ? form.querySelector('button[type="submit"]') : null;
  if (save) save.disabled = true; // re-enabled once a file is chosen

  input.addEventListener("change", function () {
    if (!input.files || !input.files[0]) return;
    if (save) save.disabled = false;

    var reader = new FileReader();
    reader.onload = function (e) {
      var current = document.getElementById("contact-avatar");
      if (!current) return;
      if (current.tagName === "IMG") {
        current.src = e.target.result;
        return;
      }
      // Replace the initials placeholder with an image preview.
      var img = document.createElement("img");
      img.className = "avatar avatar-lg";
      img.id = "contact-avatar";
      img.width = 128;
      img.height = 128;
      img.alt = "";
      img.src = e.target.result;
      current.replaceWith(img);
    };
    reader.readAsDataURL(input.files[0]);
  });
});
