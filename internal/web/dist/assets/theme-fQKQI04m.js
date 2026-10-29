// Before the first paint: the Theme setting's scheme on <html> (lib/theme.ts applies the same rule
// once the app loads), so a dark page never flashes light. A file, not inline: the CSP allows only
// scripts from self.
(function () {
  var stored = null;
  try {
    stored = localStorage.getItem('uam.theme');
  } catch {
    // Storage refused: Match system.
  }
  var dark = stored === 'dark' || (stored !== 'light' && window.matchMedia('(prefers-color-scheme: dark)').matches);
  document.documentElement.dataset.theme = dark ? 'dark' : 'light';
})();
