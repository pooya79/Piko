// Use the actual render instant and Iran's civil date, regardless of browser timezone.
// No chart runtime or sample analytics are loaded for an empty Dashboard.
const renderPersianDates = () => document.querySelectorAll('[data-persian-date]').forEach(function (element) {
  const date = new Date(element.dateTime);
  if (!Number.isNaN(date.getTime())) {
    element.textContent = new Intl.DateTimeFormat('fa-IR-u-ca-persian', {
      dateStyle: 'full', timeZone: 'Asia/Tehran'
    }).format(date);
  }
});
renderPersianDates();
document.addEventListener('piko:studio-updated', renderPersianDates);

(() => {
  const navigation = document.querySelector('#workspace-navigation');
  const trigger = document.querySelector('[data-navigation-open]');
  const mobile = window.matchMedia('(max-width: 767px)');
  const notifications = document.querySelector('#workspace-notifications');
  const notificationTrigger = document.querySelector('[popovertarget="workspace-notifications"]');

  notifications.addEventListener('toggle', () => {
    notificationTrigger.setAttribute('aria-expanded', String(notifications.matches(':popover-open')));
  });
  notifications.addEventListener('focusout', (event) => {
    if (!notifications.contains(event.relatedTarget) && event.relatedTarget !== notificationTrigger) {
      notifications.hidePopover();
    }
  });

  const updateDrawerState = () => {
    const modal = navigation.matches(':modal');
    trigger.setAttribute('aria-expanded', String(modal));
    document.body.classList.toggle('piko-drawer-open', modal);
  };
  const syncViewport = () => {
    const wasModal = navigation.matches(':modal');
    if (mobile.matches || wasModal) navigation.close();
    if (!mobile.matches) {
      // The same navigation is non-modal on desktop, retaining native links/forms.
      navigation.setAttribute('open', '');
      if (wasModal) navigation.querySelector('a').focus();
    }
    updateDrawerState();
  };
  trigger.addEventListener('click', () => {
    if (!mobile.matches) return;
    notifications.hidePopover();
    navigation.showModal();
    updateDrawerState();
  });
  navigation.querySelector('[data-navigation-close]').addEventListener('click', () => navigation.close());
  navigation.addEventListener('close', updateDrawerState);
  // Native dialogs handle Escape, focus return and background inertness. Wrap Tab
  // at the edges as well, so keyboard traversal stays in the drawer, not browser chrome.
  navigation.addEventListener('keydown', (event) => {
    if (event.key !== 'Tab' || !navigation.matches(':modal')) return;
    const controls = [...navigation.querySelectorAll('a[href], button:not(:disabled)')];
    const first = controls[0], last = controls[controls.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  });
  // A backdrop press must both start and end outside, so a drag cannot dismiss it.
  const outside = (event) => {
    const rect = navigation.getBoundingClientRect();
    return event.clientX < rect.left || event.clientX > rect.right ||
      event.clientY < rect.top || event.clientY > rect.bottom;
  };
  let backdropPressed = false;
  navigation.addEventListener('pointerdown', (event) => {
    backdropPressed = event.target === navigation && outside(event);
  });
  navigation.addEventListener('pointerup', (event) => {
    if (backdropPressed && event.target === navigation && outside(event)) navigation.close();
    backdropPressed = false;
  });
  mobile.addEventListener('change', syncViewport);
  syncViewport();
})();
