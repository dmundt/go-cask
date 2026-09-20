(() => {
  const colorScheme = window.matchMedia("(prefers-color-scheme: dark)");

  function applyColorScheme(event) {
    document.body.setAttribute(
      "data-md-color-scheme",
      event.matches ? "slate" : "default"
    );
  }

  applyColorScheme(colorScheme);
  colorScheme.addEventListener("change", applyColorScheme);
})();
