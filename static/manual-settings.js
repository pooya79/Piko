(() => {
  // Native validation must be able to focus fields inside collapsed Forms.
  document.querySelector('.piko-draft-form')?.addEventListener('invalid', event => {
    let details = event.target.closest('details');
    while (details) {
      details.open = true;
      details = details.parentElement.closest('details');
    }
  }, true);
})();
