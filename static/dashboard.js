// Use the actual render instant and Iran's civil date, regardless of browser timezone.
// No chart runtime or sample analytics are loaded for an empty Dashboard.
document.querySelectorAll('[data-persian-date]').forEach(function (element) {
  const date = new Date(element.dateTime);
  if (!Number.isNaN(date.getTime())) {
    element.textContent = new Intl.DateTimeFormat('fa-IR-u-ca-persian', {
      dateStyle: 'full', timeZone: 'Asia/Tehran'
    }).format(date);
  }
});
