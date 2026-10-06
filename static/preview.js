// Bring the latest simulated messages into view after a full-page POST/redirect.
// The form remains usable without JavaScript.
(() => {
 document.addEventListener('submit', event => {
  const pane = event.target.closest('[data-studio-preview]');
  if (pane) {
   pane.setAttribute('aria-busy', 'true');
   pane.querySelector('[data-preview-activity]').hidden = false;
  }
 });
 const transcript = document.querySelector('.piko-preview-transcript');
 const menu = document.querySelector('[data-preview-revision]');
 if (!transcript || !menu) return;
 const showLatest = () => { transcript.scrollTop = transcript.scrollHeight; };
 showLatest();
 document.fonts.ready.then(showLatest);
 new ResizeObserver(showLatest).observe(transcript);
 if (Number(menu.dataset.previewRevision) > 1) {
  (document.querySelector('#preview-answer') || menu.querySelector('button'))?.focus();
 }
})();
