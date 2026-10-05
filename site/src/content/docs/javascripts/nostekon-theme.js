(() => {
  const key = "nostekon-theme";

  const applySharedTheme = () => {
    let theme;
    try {
      theme = localStorage.getItem(key);
    } catch {
      return;
    }
    if (theme !== "light" && theme !== "dark") return;
    const palette = document.getElementById(theme === "light" ? "__palette_0" : "__palette_1");
    if (palette instanceof HTMLInputElement && !palette.checked) palette.click();
  };

  document.addEventListener("change", (event) => {
    const palette = event.target;
    if (!(palette instanceof HTMLInputElement) || palette.name !== "__palette" || !palette.checked) return;
    try {
      localStorage.setItem(key, palette.dataset.mdColorScheme === "default" ? "light" : "dark");
    } catch {
      return;
    }
  });

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", applySharedTheme, { once: true });
  } else {
    applySharedTheme();
  }
})();