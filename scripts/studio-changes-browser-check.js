// Run against an isolated database and studio-browser-provider.mjs, from /tmp.
async page => {
  const origin = page.url().match(/^https?:\/\/[^/]+/)[0];
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  const check = (value, reason) => { if (!value) throw new Error(reason); };
  const changes = () => page.locator('#studio-changes-panel');
  const preview = () => page.locator('[data-studio-preview]');
  const refresh = () => page.evaluate(() => window.pikoStudio.refresh());
  const waitRevision = revision => page.waitForFunction(revision => document.querySelector('[data-changes-revision]')?.dataset.changesRevision === revision, String(revision));
  const send = async message => {
    await page.locator('#builder-message').fill(message);
    await page.locator('#builder-message').press('Control+Enter');
    await page.locator('[data-builder-stream-url]').waitFor();
  };
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(origin + '/register');
  await page.locator('#display-name').fill('آزمایش تغییرات پیکو');
  await page.locator('#email').fill(`changes-${Date.now()}@example.test`);
  await page.locator('#password').fill('ChangesBrowser123');
  await page.locator('form[action="/register"] button').click();
  await page.waitForURL('**/dashboard');
  await page.goto(origin + '/builder');
  await send('STUDIO_BUILD یک ربات بساز');
  check(await changes().count() === 0, 'provisional creation displays Changes');
  await waitRevision(1);
  await page.locator('#studio-tab-changes').click();
  check(await changes().locator('[data-change-result="created"] [data-change-action="added"]').count() > 0, 'initial saved changes missing');
  check(await changes().locator('form[action$="/undo"]').count() === 0, 'creation offers Undo without a prior Draft');
  const botPath = page.url().slice(origin.length).match(/^\/bots\/\d+/)[0];
  const manualDefinition = {
    version: 2,
    welcome: { id: 'welcome', type: 'message', text: 'خوش‌آمد دستی <script>unsafe</script>' },
    menu: { id: 'menu', type: 'menu', text: 'منوی دستی', choices: [{ id: 'collect', label: 'فرم آزمایش', target: 'custom' }] },
    messages: [],
    forms: [{ id: 'custom', review: 'مرور دستی', acknowledgement: 'تأیید دستی', questions: [
      { id: 'name', label: 'نام', prompt: 'نام دستی؟', type: 'short_text', required: true },
      { id: 'extra', label: 'توضیح اضافه', prompt: 'توضیح؟', type: 'long_text', required: false },
    ] }],
  };
  const manualSave = revision => page.evaluate(async ({ botPath, revision, definition }) => {
    const csrf_token = document.querySelector('input[name="csrf_token"]').value;
    const response = await fetch(botPath + '/draft', { method: 'POST', body: new URLSearchParams({ csrf_token, definition: JSON.stringify(definition), draft_revision: String(revision) }) });
    if (!response.ok) throw new Error('manual save failed');
  }, { botPath, revision, definition: manualDefinition });
  await manualSave(1);
  await refresh();
  await waitRevision(2);
  await page.locator('#studio-tab-preview').click();
  await preview().locator('form button[type="submit"]').click();
  await preview().locator('[data-preview-source-revision="2"]').waitFor();
  const previewURL = await preview().getAttribute('data-preview-url');
  await page.locator('#studio-tab-changes').click();
  await page.evaluate(() => { window.changesNavigationProbe = true; });
  await send('STUDIO_EDIT تغییر بده');
  const running = changes().locator('[data-change-status="running"]');
  await running.waitFor();
  check(await running.locator('[data-change-action], form[action$="/undo"]').count() === 0, 'provisional candidate claims a save');
  await page.locator('#builder-message').fill('پیام بعدی حفظ شود');
  await page.locator('#builder-message').evaluate(el => el.setSelectionRange(2, 5, 'backward'));
  await waitRevision(3);
  check(await page.locator('#builder-message').evaluate(el => el === document.activeElement && el.value === 'پیام بعدی حفظ شود' && el.selectionStart === 2 && el.selectionEnd === 5 && el.selectionDirection === 'backward'), 'completion lost composer focus/text/caret');
  check(await changes().isVisible(), 'completion lost Changes tab');
  const saved = changes().locator('[data-change-result="saved"]').first();
  check(await saved.locator('[data-change-action="edited"]').count() > 0 && await saved.locator('[data-change-action="removed"]').count() > 0, 'saved diff missing edit/removal');
  check(!(await saved.innerText()).includes('پاسخ ذخیره شده پیکو'), 'Changes derived from assistant prose');
  await saved.locator('summary').first().click();
  const detailID = await saved.locator('details').first().getAttribute('id');
  await refresh();
  check(await page.locator('#' + detailID).getAttribute('open') !== null, 'refresh lost inspected change');
  await page.locator('#studio-tab-preview').click();
  check(await preview().getAttribute('data-preview-url') === previewURL, 'commit replaced selected Preview');
  check(await preview().locator('[data-preview-stale="true"]').count() === 1, 'commit did not stale Preview');
  // Start a Preview of the saved result, so Undo must stale that identified test.
  await preview().locator(`form[action="${botPath}/preview"] button`).click();
  await preview().locator('[data-preview-source-revision="3"]').waitFor();
  await page.locator('#studio-tab-changes').click();
  await saved.locator('form[action$="/undo"] button').focus();
  await page.keyboard.press('Enter');
  await waitRevision(4);
  check(await changes().locator('[data-change-result="undone"]').count() === 1, 'Undo outcome missing');
  check(await changes().locator('form[action$="/undo"]').count() === 0, 'Undo remains actionable');
  check(await page.locator('#builder-message').inputValue() === 'پیام بعدی حفظ شود', 'Undo erased unsent prose');
  check(await page.evaluate(() => document.activeElement !== document.body && window.changesNavigationProbe), 'Undo navigated or lost focus');
  await page.locator('#studio-tab-preview').click();
  check(await preview().locator('[data-preview-source-revision="3"][data-preview-stale="true"]').count() === 1, 'Undo did not stale Preview');
  await page.locator('#studio-tab-changes').click();
  // A newer manual revision removes the action and the service rejects a stale POST.
  await send('STUDIO_EDIT تغییر دوم');
  await waitRevision(5);
  const staleAction = await changes().locator('form[action$="/undo"]').getAttribute('action');
  await manualSave(5);
  await refresh();
  await waitRevision(6);
  check(await changes().locator('form[action$="/undo"]').count() === 0, 'manual edit leaves Undo actionable');
  check(await changes().locator('[data-undo-unavailable]').count() > 0, 'guard explanation missing');
  const staleCode = await page.evaluate(async action => {
    const csrf_token = document.querySelector('input[name="csrf_token"]').value;
    const response = await fetch(action, { method: 'POST', headers: { 'X-Piko-Studio': 'fragment' }, body: new URLSearchParams({ csrf_token }) });
    return response.status;
  }, staleAction);
  check(staleCode === 409, 'stale Undo overwrote newer work');
  await send('STUDIO_FAIL پاسخ ناموفق');
  await page.locator('[data-builder-stream-url]').waitFor({ state: 'detached' });
  check(await changes().locator('[data-change-status="failed"]').first().locator('[data-change-action], form').count() === 0, 'failed result fabricates changes');
  await page.locator('#builder-message').fill('متن برای موبایل');
  for (const mobile of [false, true]) {
    await page.setViewportSize(mobile ? { width: 390, height: 844 } : { width: 1440, height: 1000 });
    if (mobile) await page.locator('[data-studio-view="pane"]').click();
    await page.locator('#studio-tab-changes').click();
    for (const mode of ['light', 'dark', 'system']) {
      await page.emulateMedia({ colorScheme: mode === 'light' ? 'light' : 'dark', reducedMotion: 'reduce' });
      await page.evaluate(mode => {
        localStorage.setItem('piko-theme', mode);
        document.documentElement.dataset.theme = mode === 'system' ? (matchMedia('(prefers-color-scheme: dark)').matches ? 'piko-dark' : 'piko-light') : 'piko-' + mode;
      }, mode);
      await refresh();
      check(await changes().isVisible(), 'refresh lost mobile Changes pane');
      check(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'Changes overflows the document');
      await page.locator('#studio-pane').evaluate(el => { el.scrollTop = 0; });
      await page.screenshot({ path: `/tmp/piko-changes-${mobile ? 'mobile' : 'desktop'}-${mode}.png` });
    }
    if (mobile) {
      await page.locator('[data-studio-view="conversation"]').click();
      check(await page.locator('#builder-message').inputValue() === 'متن برای موبایل', 'mobile switching lost prose');
    }
  }
  check(errors.length === 0, errors.join('; '));
  return { passed: true, chatURL: page.url(), previewURL, errors };
}
