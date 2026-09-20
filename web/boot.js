// Runs before the first paint, as a classic blocking script in <head>: the
// parser stops here until it has run, so the remembered theme, accent,
// density and sidebar state are on <html> before anything is drawn and
// nothing flashes. It lives in its own file rather than inline so the
// Content-Security-Policy can stay at 'self' with no hash to keep in step
// with this code. Settings from the service override all of it once the app
// has loaded.
(function () {
  try {
    var t = localStorage.getItem('gw.theme') || 'dark';
    if (t === 'system') t = window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
    document.documentElement.setAttribute('data-theme', t);
    var a = localStorage.getItem('gw.accent');  // default is #43c9c0, set in app.css
    if (a && /^#[0-9a-f]{6}$/i.test(a)) {
      var r = parseInt(a.slice(1, 3), 16), g = parseInt(a.slice(3, 5), 16), b = parseInt(a.slice(5, 7), 16);
      document.documentElement.style.setProperty('--accent-rgb', r + ', ' + g + ', ' + b);
    }
    // #32: compact is the default, so only "comfortable" needs an attribute
    // at all — but setting it explicitly either way means app.css never has
    // to guess before boot.js has run.
    var d = localStorage.getItem('gw.density') === 'comfortable' ? 'comfortable' : 'compact';
    document.documentElement.setAttribute('data-density', d);
    if (localStorage.getItem('gw.railPinned') === '1') document.documentElement.classList.add('rail-pinned');
  } catch (e) { /* storage unavailable */ }
})();
