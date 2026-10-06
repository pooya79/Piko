// Run with playwright-cli against an isolated app and studio-browser-provider.
// Save browser output in /tmp; this exercises synthetic data and no live APIs.
async page => {
  const origin = page.url().match(/^https?:\/\/[^/]+/)[0];
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  const check = (value, reason) => { if (!value) throw new Error(reason); };
  const preview = () => page.locator('[data-studio-preview]');
  const source = () => preview().locator('[data-preview-source-revision]');
  const progress = () => preview().locator('[data-preview-revision]');
  const waitProgress = revision => page.waitForFunction(revision => document.querySelector('[data-preview-revision]')?.dataset.previewRevision === revision, String(revision));
  const refresh = () => page.evaluate(() => window.pikoStudio.refresh());
  const choose = async (id, revision) => {
    await preview().locator(`button[name="choice"][value="${id}"]`).click();
    await waitProgress(revision);
  };
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(origin + '/dashboard');
  if (page.url().endsWith('/login')) {
    await page.goto(origin + '/register');
    await page.locator('#display-name').fill('آزمایش پیش‌نمایش پیکو');
    await page.locator('#email').fill(`preview-${Date.now()}@example.test`);
    await page.locator('#password').fill('PreviewBrowser123');
    await page.locator('form[action="/register"] button').click();
    await page.waitForURL('**/dashboard');
  }
  await page.goto(origin + '/builder');
  await page.evaluate(() => { window.previewNavigationProbe = true; });
  await page.locator('#builder-message').fill('STUDIO_BUILD یک ربات برای جمع آوری نام بساز');
  await page.locator('#builder-message').press('Control+Enter');
  await page.locator('[data-draft-revision]').waitFor();
  await page.locator('[data-studio-outcome="succeeded"]').waitFor({ state: 'attached' });
  check(await page.evaluate(() => window.previewNavigationProbe), 'initial build navigated');
  const chatURL = page.url();
  const botPath = chatURL.slice(origin.length).match(/^\/bots\/\d+/)[0];
  await preview().locator('form button[type="submit"]').click();
  await waitProgress(1);
  const firstURL = await preview().getAttribute('data-preview-url');
  check(await source().getAttribute('data-preview-source-revision') === '1', 'source revision missing');
  await choose('collect', 2);
  await page.locator('#preview-answer').fill('پاسخ هنوز ارسال نشده');
  await page.locator('#builder-message').fill('STUDIO_EDIT پیش نویس را به روز کن');
  await page.locator('#builder-message').press('Control+Enter');
  await page.locator('[data-builder-stream-url]').waitFor();
  await page.locator('#builder-message').fill('پیام بعدی برای پیکو');
  await page.locator('#preview-answer').focus();
  await page.locator('#preview-answer').evaluate(el => el.setSelectionRange(2, 5, 'backward'));
  await page.locator('[data-builder-stream-url]').waitFor({ state: 'detached' });
  check(await preview().getAttribute('data-preview-url') === firstURL, 'run replaced selected Preview');
  check(await progress().getAttribute('data-preview-revision') === '2', 'run advanced Preview progress');
  check(await page.locator('#preview-answer').inputValue() === 'پاسخ هنوز ارسال نشده', 'run lost unsent answer');
  check(await page.locator('#preview-answer').evaluate(el => el === document.activeElement && el.selectionStart === 2 && el.selectionEnd === 5 && el.selectionDirection === 'backward'), 'run lost answer focus/caret');
  check(await page.locator('#builder-message').inputValue() === 'پیام بعدی برای پیکو', 'Preview lost composer');
  check(await source().getAttribute('data-preview-stale') === 'true', 'commit did not visibly stale Preview');
  check(await preview().locator('[data-preview-stale-notice]').isVisible(), 'stale warning hidden');

  // Same snapshot restart versus a new snapshot from a visibly changed Draft.
  const save = async (definition, revision) => {
    const csrf_token = await page.locator('[name="csrf_token"]').first().inputValue();
    const chat = await page.request.post(origin + botPath + '/chats', { form: { csrf_token, title: 'Browser fixture setup' } });
    check(chat.ok(), 'fixture chat creation failed');
    const editing = await page.context().newPage();
    try {
      const chatURL = chat.url();
      const response = await editing.request.post(chatURL + '/messages', { form: { csrf_token, message: 'STUDIO_DEFINITION ' + JSON.stringify(definition) } });
      check(response.ok(), 'fixture Builder request failed');
      await editing.goto(chatURL);
      await editing.locator(`[data-draft-revision="${revision + 1}"]`).waitFor();
    } finally { await editing.close(); }
  };
  await save({ version: 2, welcome: { id: 'welcome', type: 'message', text: 'سلام از نسخه تازه ذخیره شده' }, menu: { id: 'menu', type: 'menu', text: 'انتخاب کنید', choices: [{ id: 'collect', label: 'فرم آزمایش', target: 'custom' }] }, messages: [], forms: [{ id: 'custom', review: 'بررسی کنید', acknowledgement: 'دریافت شد', questions: [{ id: 'name', label: 'نام', prompt: 'نام شما؟', type: 'short_text', required: true }] }] }, 2);
  await refresh();
  await preview().locator('form[action$="/restart"] button').click();
  await waitProgress(3);
  check(await source().getAttribute('data-preview-source-revision') === '1', 'restart changed source');
  check(!(await preview().innerText()).includes('سلام از نسخه تازه ذخیره شده'), 'restart changed snapshot');
  await preview().locator(`form[action="${botPath}/preview"] button`).click();
  await waitProgress(1);
  check(await source().getAttribute('data-preview-source-revision') === '3', 'fresh test has wrong source');
  check((await preview().innerText()).includes('سلام از نسخه تازه ذخیره شده'), 'fresh test did not load saved behavior');
  const freshURL = await preview().getAttribute('data-preview-url');
  await choose('collect', 2);
  await page.locator('#preview-answer').fill('مینا آزمایشی');
  await preview().locator('.piko-preview-answer button').focus();
  await page.keyboard.press('Enter');
  await waitProgress(3);
  await choose('edit', 4);
  await choose('edit:name', 5);
  await page.locator('#preview-answer').fill('نام ویرایش شده');
  await preview().locator('.piko-preview-answer button').click();
  await waitProgress(6);
  await choose('submit', 7);
  check((await preview().innerText()).includes('دریافت شد'), 'confirmation missing');
  check(!(await (await page.request.get(origin + botPath + '/submissions')).text()).includes('data-submission-id='), 'Preview created a real Submission');
  check(await page.evaluate(() => window.previewNavigationProbe), 'Preview action navigated');

  // Reload observes the selected test; it never creates another one.
  await page.reload();
  await waitProgress(7);
  check(await preview().getAttribute('data-preview-url') === freshURL, 'reload changed Preview selection');
  await page.locator('#builder-message').fill('متن پیکو حفظ می‌شود');
  await preview().locator('form[action$="/restart"] button').click();
  await waitProgress(8);
  await choose('collect', 9);
  await page.locator('#preview-answer').fill('پاسخ ناتمام برای ادامه');
  await page.route('**/preview/*/choose', route => route.abort(), { times: 1 });
  await preview().locator('.piko-preview-answer button').click();
  await preview().locator('[data-preview-load-error]').waitFor({ state: 'visible' });
  check(await page.locator('#preview-answer').inputValue() === 'پاسخ ناتمام برای ادامه', 'failed request lost answer');
  await preview().locator('[data-preview-retry]').click();
  await preview().locator('[data-preview-load-error]').waitFor({ state: 'hidden' });
  check(await progress().getAttribute('data-preview-revision') === '9', 'recovery repeated failed POST');
  check(await page.locator('#preview-answer').inputValue() === 'پاسخ ناتمام برای ادامه', 'read-only recovery lost unsent answer');

  // A response waits for active text composition before replacing its textarea.
  await page.route('**/preview/*/choose', route => route.abort(), { times: 1 });
  await preview().locator('.piko-preview-answer button').click();
  await preview().locator('[data-preview-load-error]').waitFor({ state: 'visible' });
  await page.locator('#preview-answer').evaluate(el => {
    el.dataset.compositionProbe = 'original';
    el.dispatchEvent(new CompositionEvent('compositionstart', { bubbles: true }));
  });
  await preview().locator('[data-preview-retry]').click();
  await page.locator('#preview-answer').focus();
  await page.locator('#preview-answer').evaluate(el => el.setSelectionRange(2, 4));
  await page.waitForTimeout(350);
  check(await page.locator('#preview-answer').getAttribute('data-composition-probe') === 'original', 'recovery replaced active composition');
  await page.locator('#preview-answer').evaluate(el => el.dispatchEvent(new CompositionEvent('compositionend', { bubbles: true })));
  await preview().locator('[data-preview-load-error]').waitFor({ state: 'hidden' });
  check(await page.locator('#preview-answer').evaluate(el => el === document.activeElement && el.selectionStart === 2 && el.selectionEnd === 4), 'read-only recovery lost moved answer focus');

  // Delayed Preview actions respect later composer focus and mobile selection.
  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator('[data-studio-view="pane"]').click();
  await page.route('**/preview/*/choose', async route => {
    await page.waitForTimeout(500);
    await route.continue();
  }, { times: 1 });
  await preview().locator('.piko-preview-answer button').click();
  await page.locator('[data-studio-view="conversation"]').click();
  await page.locator('#builder-message').fill('متن پیکو حفظ می‌شود');
  await page.locator('#builder-message').evaluate(el => el.setSelectionRange(2, 4));
  await waitProgress(10);
  check(await page.locator('#builder-message').evaluate(el => el === document.activeElement && el.selectionStart === 2 && el.selectionEnd === 4), 'delayed response stole composer focus');
  check(await page.locator('[data-piko-studio]').getAttribute('data-view') === 'conversation', 'delayed response switched mobile view');
  await page.locator('[data-studio-view="pane"]').click();
  await preview().locator('form[action$="/restart"] button').click();
  await waitProgress(11);
  await choose('collect', 12);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.locator('#preview-answer').fill('نام برای بررسی');
  await preview().locator('.piko-preview-answer button').click();
  await waitProgress(13);
  for (let revision = 14; revision <= 17; revision += 3) {
    await choose('edit', revision);
    await choose('edit:name', revision + 1);
    await page.locator('#preview-answer').fill('نام ویرایش شده برای بررسی');
    await preview().locator('.piko-preview-answer button').click();
    await waitProgress(revision + 2);
  }
  check(await preview().locator('.piko-preview-transcript').evaluate(el => el.scrollHeight > el.clientHeight), 'scroll scenario needs a longer transcript');
  await preview().locator('.piko-preview-transcript').evaluate(el => { el.scrollTop = el.scrollHeight; });
  await page.route('**/preview/*/choose', async route => {
    await page.waitForTimeout(500);
    await route.continue();
  }, { times: 1 });
  await preview().locator('button[name="choice"][value="edit"]').click();
  await preview().locator('.piko-preview-transcript').evaluate(el => { el.scrollTop = 0; });
  await waitProgress(20);
  check(await preview().locator('.piko-preview-transcript').evaluate(el => el.scrollTop) === 0, 'delayed response erased newer reading position');
  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator('[data-studio-view="pane"]').click();
  await preview().locator('.piko-preview-transcript').evaluate(el => { el.scrollTop = 50; });
  await page.route('**/preview/*/choose', async route => {
    await page.waitForTimeout(500);
    await route.continue();
  }, { times: 1 });
  await preview().locator('button[name="choice"][value="edit:name"]').click();
  await page.locator('[data-studio-view="conversation"]').click();
  await waitProgress(21);
  await page.locator('[data-studio-view="pane"]').click();
  check(await preview().locator('.piko-preview-transcript').evaluate(el => el.scrollTop) === 50, 'hidden response lost transcript reading position');
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.locator('#preview-answer').fill('پاسخ ناتمام در نمای موبایل');

  // Four display modes, mobile pane selection, keyboard and reading preservation.
  for (const mobile of [false, true]) {
    await page.setViewportSize(mobile ? { width: 390, height: 844 } : { width: 1440, height: 1000 });
    if (mobile) await page.locator('[data-studio-view="pane"]').click();
    await page.locator('#preview-answer').focus();
    await page.locator('#preview-answer').evaluate(el => el.setSelectionRange(2, 4));
    await preview().locator('.piko-preview-transcript').evaluate(el => { el.scrollTop = 0; });
    // Bring the real question and keyboard controls into the mobile viewport.
    await page.locator('#preview-answer').scrollIntoViewIfNeeded();
    for (const dark of [false, true]) {
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await page.evaluate(dark => { document.documentElement.dataset.theme = dark ? 'piko-dark' : 'piko-light'; }, dark);
      await refresh();
      check(await page.locator('#preview-answer').inputValue() === 'پاسخ ناتمام در نمای موبایل', 'fragment lost local answer');
      check(await page.locator('#preview-answer').evaluate(el => el === document.activeElement && el.selectionStart === 2 && el.selectionEnd === 4), 'fragment lost keyboard focus');
      check(await preview().locator('.piko-preview-transcript').evaluate(el => el.scrollTop) === 0, 'fragment moved transcript');
      check(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'document overflows');
      const mode = `${mobile ? 'mobile' : 'desktop'}-${dark ? 'dark' : 'light'}`;
      await page.screenshot({ path: `/tmp/piko40-browser-output/${mode}-controls.png` });
      const paneTop = await page.locator('#studio-pane').evaluate(el => el.scrollTop);
      await page.locator('#studio-pane').evaluate(el => { el.scrollTop = 0; });
      await page.screenshot({ path: `/tmp/piko40-browser-output/${mode}.png` });
      await page.locator('#studio-pane').evaluate((el, top) => { el.scrollTop = top; }, paneTop);
    }
    if (mobile) {
      await page.locator('[data-studio-view="conversation"]').click();
      check(await page.locator('#builder-message').inputValue() === 'متن پیکو حفظ می‌شود', 'mobile switching lost composer');
    }
  }
  check(errors.length === 0, errors.join('; '));
  return { passed: true, chatURL, firstURL, freshURL, screenshots: '/tmp/piko40-browser-output', errors };
}
