// Bring the latest simulated messages into view after a full-page POST/redirect.
// The form remains usable without JavaScript.
(() => {
 const transcript = document.querySelector('.piko-preview-transcript');
 const menu = document.querySelector('[data-preview-revision]');
 if (!transcript || !menu) return;
 const showLatest = () => { transcript.scrollTop = transcript.scrollHeight; };
 showLatest();
 document.fonts.ready.then(showLatest);
 new ResizeObserver(showLatest).observe(transcript);
 if (Number(menu.dataset.previewRevision) > 1) {
  menu.querySelector('button')?.focus();
 }
})();
