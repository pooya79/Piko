// Apply the saved choice before CSS loads; system remains the default preference.
(() => {
  const root = document.documentElement;
  const media = window.matchMedia("(prefers-color-scheme: dark)");
  let choice = "system";
  try {
    const stored = window.localStorage.getItem("piko-theme");
    if (stored === "light" || stored === "dark") choice = stored;
  } catch (_) {
    // A blocked storage API should not prevent system theme selection.
  }

  const apply = () => {
    const dark = choice === "dark" || (choice === "system" && media.matches);
    root.dataset.theme = dark ? "piko-dark" : "piko-light";
    root.dataset.themeMode = choice;
    document.querySelectorAll("[data-theme-choice]").forEach((button) => {
      button.setAttribute("aria-pressed", String(button.dataset.themeChoice === choice));
    });
  };

  apply();
  media.addEventListener("change", apply);
  document.addEventListener("DOMContentLoaded", apply);
  document.addEventListener("click", (event) => {
    const button = event.target.closest("[data-theme-choice]");
    if (!button) return;
    choice = button.dataset.themeChoice;
    try {
      if (choice === "system") window.localStorage.removeItem("piko-theme");
      else window.localStorage.setItem("piko-theme", choice);
    } catch (_) {
      // The choice still works for this page when storage is unavailable.
    }
    apply();
  });
})();
