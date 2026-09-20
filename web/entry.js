// The app's entry point. The in-browser mock backend (mock.js) is only loaded
// when the page is opened with ?mock=1; it installs itself over fetch before
// app.js is imported, so the app never knows the difference.
if (/(^|[?&])mock=1(&|$)/.test(location.search)) {
  await import('./mock.js');
}
await import('./app.js');
