(() => {
  const marker = document.querySelector('[data-builder-stream-url]');
  if (!marker) return;
  const progress = document.querySelector('[data-builder-progress]');
  const reply = document.querySelector('[data-builder-reply]');
  const connection = document.querySelector('[data-builder-connection]');
  let stream;
  function connect() {
    if (!window.EventSource) {
      connection.textContent = connection.dataset.unavailable;
      return;
    }
    stream = new EventSource(marker.dataset.builderStreamUrl);
    stream.addEventListener('snapshot', event => {
      const run = JSON.parse(event.data);
      connection.textContent = '';
      if (String(run.id) !== marker.dataset.builderRunId || run.status !== 'running') {
        stream.close();
        window.location.reload();
        return;
      }
      // Provisional text is plain text, never HTML or committed Draft feedback.
      reply.textContent = run.text;
      progress.textContent = run.progress;
    });
    stream.addEventListener('error', () => {
      connection.textContent = stream.readyState === EventSource.CLOSED
        ? connection.dataset.unavailable : connection.dataset.reconnecting;
    });
  }
  window.addEventListener('pagehide', () => stream?.close());
  window.addEventListener('pageshow', event => { if (event.persisted) connect(); });
  connect();
})();
