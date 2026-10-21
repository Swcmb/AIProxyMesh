export type Theme = "dark" | "light";

const STORAGE_KEY = "mesh.theme";

export function readTheme(): Theme {
  return localStorage.getItem(STORAGE_KEY) === "light" ? "light" : "dark";
}

export function applyTheme(theme: Theme) {
  document.documentElement.dataset.theme = theme;
  localStorage.setItem(STORAGE_KEY, theme);
}

export function initTheme(): Theme {
  const theme = readTheme();
  document.documentElement.dataset.theme = theme;
  return theme;
}
