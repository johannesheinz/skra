// Progressive enhancement for the contact photo control. Without JS, the file
// input's Save button uploads the chosen file. With JS, the avatar's camera
// badge opens the picker and choosing a file previews it and uploads
// immediately — there is no separate Save step. The preview uses a data: URL
// (FileReader) so it complies with the img-src 'self' data: CSP.
document.addEventListener("DOMContentLoaded", function () {
  var input = document.querySelector("[data-photo-input]");
  if (!input) return;
  var form = input.closest("[data-photo-form]");

  input.addEventListener("change", function () {
    if (!input.files || !input.files[0]) return;

    var reader = new FileReader();
    reader.onload = function (e) {
      var current = document.getElementById("contact-avatar");
      if (current) {
        if (current.tagName === "IMG") {
          current.src = e.target.result;
        } else {
          // Replace the initials placeholder with an image preview.
          var img = document.createElement("img");
          img.className = "avatar avatar-lg";
          img.id = "contact-avatar";
          img.width = 128;
          img.height = 128;
          img.alt = "";
          img.src = e.target.result;
          current.replaceWith(img);
        }
      }
      // Upload right away; the brief preview is what the user sees mid-flight.
      if (form) {
        if (form.requestSubmit) form.requestSubmit();
        else form.submit();
      }
    };
    reader.readAsDataURL(input.files[0]);
  });
});
