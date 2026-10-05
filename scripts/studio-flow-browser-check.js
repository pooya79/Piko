// Run through playwright-cli against the isolated studio app/provider fixture.
// All screenshots and browser state belong under /tmp.
async page => {
  const origin = page.url().match(/^https?:\/\/[^/]+/)[0];
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.route('**/static/**', route => route.continue());
  const check = (value, reason) => { if (!value) throw new Error(reason); };
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(origin + '/dashboard');
  if (page.url().endsWith('/login')) {
    await page.goto(origin + '/register');
    await page.locator('#display-name').fill('آزمایش جریان پیکو');
    await page.locator('#email').fill(`flow-${Date.now()}@example.test`);
    await page.locator('#password').fill('FlowBrowser123');
    await page.locator('form[action="/register"] button').click();
    await page.waitForURL('**/dashboard');
  }
  const botPath = await page.evaluate(async () => {
    const csrf_token = document.querySelector('input[name="csrf_token"]').value;
    const response = await fetch('/bots/new', { method: 'POST', body: new URLSearchParams({ csrf_token, name: 'ربات بررسی جریان' }) });
    if (!response.ok) throw new Error('create failed');
    return new URL(response.url).pathname.match(/^\/bots\/\d+/)[0];
  });
  const chatPath = await page.evaluate(async botPath => {
    const csrf_token = document.querySelector('input[name="csrf_token"]').value;
    const response = await fetch(botPath + '/chats', { method: 'POST', body: new URLSearchParams({ csrf_token, title: 'بررسی بخش‌های جریان' }) });
    if (!response.ok) throw new Error('chat failed');
    return new URL(response.url).pathname;
  }, botPath);
  const definition = {
    version: 2, welcome: { id: 'welcome', type: 'message', text: 'سلام! درخواستتان را با پیکو آماده کنید.' },
    menu: { id: 'menu', type: 'menu', text: 'از کجا شروع کنیم؟', choices: [{ id: 'apply', label: 'درخواست ثبت‌نام', target: 'application' }, { id: 'info', label: 'ساعت کار', target: 'hours' }] },
    messages: [{ id: 'hours', type: 'message', text: 'شنبه تا چهارشنبه؛ ۹ تا ۱۷' }],
    forms: [{ id: 'application', review: 'پاسخ‌ها را بررسی و درخواست را ارسال کنید.', acknowledgement: 'درخواست دریافت شد؛ پذیرش یا ظرفیت تضمین نمی‌شود.', questions: [
      { id: 'name', label: 'نام', prompt: 'نام شما چیست؟', type: 'short_text', required: true, max_length: 80 },
      { id: 'course', label: 'دوره', prompt: 'کدام دوره را می‌خواهید؟', type: 'single_choice', required: true, options: ['هنر', 'علوم'] },
      { id: 'quantity', label: 'تعداد', prompt: 'تعداد درخواستی را وارد کنید.', type: 'number', required: true, number: { min: '1', max: '10' } },
      { id: 'phone', label: 'تماس', prompt: 'شماره تماس شما چیست؟', type: 'phone', required: true },
      { id: 'date', label: 'تاریخ', prompt: 'تاریخ ترجیحی شمسی را وارد کنید.', type: 'date', required: true, date: { min: '1405/01/01', max: '1405/12/29' } },
      { id: 'details', label: 'توضیح', prompt: 'توضیح دیگری دارید؟', type: 'long_text', required: false },
    ] }],
  };
  const save = async (draft, revision) => page.evaluate(async ({ botPath, draft, revision }) => {
    const csrf_token = document.querySelector('input[name="csrf_token"]').value;
    const response = await fetch(botPath + '/draft', { method: 'POST', body: new URLSearchParams({ csrf_token, definition: JSON.stringify(draft), draft_revision: String(revision) }) });
    if (!response.ok) throw new Error('manual save failed');
  }, { botPath, draft, revision });
  await save(definition, 1);
  await page.goto(origin + chatPath);
  const flow = () => page.locator('[data-studio-flow]');
  const refresh = () => page.evaluate(() => window.pikoStudio.refresh());
  const welcomeKey = 'block:d2VsY29tZQ';
  const nameKey = 'question:YXBwbGljYXRpb24:bmFtZQ';
  const node = key => flow().locator(`[data-flow-key="${key}"]`);
  await page.getByRole('button', { name: 'جریان ربات', exact: true }).click();
  check(await flow().getAttribute('data-flow-revision') === '2', 'Flow source missing');
  check(await flow().locator('[data-flow-key]').count() === 11, 'canvas differs from committed Flow');
  const ackKey = 'ack:YXBwbGljYXRpb24';
  await node(ackKey).locator('summary').click();
  await node(ackKey).getByRole('link', { name: 'شروع دوباره با خوش‌آمد و منو', exact: true }).click();
  check(await node(welcomeKey).getAttribute('open') !== null, 'restart skipped Welcome');
  await page.getByRole('button', { name: 'بزرگ‌نمایی', exact: true }).focus();
  await page.keyboard.press('Enter');
  check(await flow().getAttribute('data-flow-zoom') === '125', 'keyboard zoom failed');
  await node(nameKey).locator('summary').focus();
  await page.keyboard.press('Enter');
  check(await node(nameKey).getAttribute('open') !== null, 'keyboard selection failed');
  check((await node(nameKey).innerText()).includes('پاسخ الزامی'), 'details unreadable');
  await page.locator('#builder-message').fill('درخواست ناتمام من');
  await node(nameKey).getByRole('button', { name: 'این بخش را با پیکو تغییر بده' }).click();
  check(await page.locator('input[name="selected_block"]').inputValue() === nameKey, 'handoff lost Question identity');
  check(await page.locator('#builder-message').inputValue() === 'درخواست ناتمام من', 'handoff overwrote unsent prose');
  check(await page.locator('#builder-message').evaluate(el => el === document.activeElement), 'handoff focus missing');
  await page.route('**' + chatPath + '/messages', async route => {
    const response = await route.fetch();
    await page.waitForTimeout(700);
    await route.fulfill({ response });
  }, { times: 1 });
  const sent = page.waitForRequest(request => request.method() === 'POST' && request.url().endsWith('/messages'));
  await page.locator('#builder-message').fill('این پرسش را توضیح بده');
  await page.locator('#builder-message').press('Control+Enter');
  const request = await page.evaluate(data => Object.fromEntries(new URLSearchParams(data)), (await sent).postData());
  check(request.selected_block === nameKey && request.selected_revision === '2', 'POST lost context');
  // A new target selected while admission is in flight belongs to the follow-up.
  await node(welcomeKey).locator('summary').click();
  await node(welcomeKey).getByRole('button', { name: 'این بخش را با پیکو تغییر بده' }).click();
  await page.locator('#builder-message').fill('پیام بعدی هنوز ارسال نشده');
  await page.locator('[data-builder-stream-url]').waitFor();
  check(await page.locator('input[name="selected_block"]').inputValue() === welcomeKey, 'admission cleared a newer target');
  await page.locator('[data-builder-stream-url]').waitFor({ state: 'detached' });
  check(await flow().getAttribute('data-flow-zoom') === '125' && await node(welcomeKey).getAttribute('open') !== null, 'completion lost valid zoom/selection');
  check(await page.locator('#builder-message').inputValue() === 'پیام بعدی هنوز ارسال نشده', 'completion lost composer');
  check(await page.locator('input[name="selected_block"]').inputValue() === welcomeKey, 'completion lost follow-up target');
  await page.locator('[data-flow-clear]').click();
  await node(nameKey).locator('summary').click();

  const screenshots = [];
  for (const size of ['desktop', 'mobile']) {
    await page.setViewportSize(size === 'desktop' ? { width: 1440, height: 1000 } : { width: 390, height: 844 });
    if (size === 'mobile') await page.locator('[data-studio-view="pane"]').click();
    for (const theme of ['light', 'dark']) {
      await page.locator(`[data-theme-choice="${theme}"]`).click();
      await page.evaluate(() => document.fonts.ready);
      check(!(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)), `${size}/${theme} page overflow`);
      await flow().locator('.piko-flow-viewport').evaluate(el => { el.scrollTop = 0; el.scrollLeft = -(el.scrollWidth - el.clientWidth) / 2; });
      const path = `/tmp/piko-flow-${size}-${theme}.png`;
      await page.screenshot({ path });
      screenshots.push(path);
    }
  }
  // Mobile inspection uses the same native keyboard controls and isolated scroll.
  await node(welcomeKey).locator('summary').focus();
  await page.keyboard.press('Enter');
  await node(welcomeKey).getByRole('button', { name: 'این بخش را با پیکو تغییر بده' }).click();
  check(await page.locator('#builder-message').isVisible() && !(await page.locator('#studio-pane').isVisible()), 'mobile handoff did not return to conversation');
  await page.locator('#builder-message').fill('STUDIO_EDIT این بخش را تغییر بده');
  await page.locator('#builder-message').press('Control+Enter');
  await page.locator('[data-builder-stream-url]').waitFor();
  check(await flow().getAttribute('data-flow-revision') === '2', 'provisional prose changed graph');
  await page.locator('[data-builder-stream-url]').waitFor({ state: 'detached' });
  check(await flow().getAttribute('data-flow-revision') === '3', 'committed run did not refresh graph');
  check(await node(welcomeKey).getAttribute('open') !== null && await flow().getAttribute('data-flow-zoom') === '125', 'valid selection lost after edit');
  check(await flow().locator('[data-flow-key]').count() === 5, 'graph retained superseded Questions');

  await page.locator('[data-studio-view="pane"]').click();
  const customKey = 'question:Y3VzdG9t:bmFtZQ';
  await node(customKey).locator('summary').click();
  await node(customKey).getByRole('button', { name: 'این بخش را با پیکو تغییر بده' }).click();
  // Commit between handoff and POST without waiting for browser reconciliation.
  await save({ version: 1, welcome: definition.welcome, menu: { ...definition.menu, choices: [{ id: 'info', label: 'ساعت کار', target: 'hours' }] }, messages: definition.messages }, 3);
  await page.locator('#builder-message').fill('درخواست برای بخش قدیمی');
  const rejected = page.waitForResponse(response => response.request().method() === 'POST' && response.url().endsWith('/messages'));
  await page.locator('#builder-message').press('Control+Enter');
  check((await rejected).status() === 409, 'stale target admitted');
  await page.waitForFunction(() => document.querySelector('[data-studio-flow]').dataset.flowRevision === '4');
  check(await page.locator('#builder-message').inputValue() === 'درخواست برای بخش قدیمی', 'stale rejection lost prose');
  check(await page.locator('input[name="selected_block"]').inputValue() === customKey, 'stale target became an unscoped request');
  check(await page.locator('[data-flow-request-stale]').isVisible(), 'stale target not explained in composer');
  const again = page.waitForResponse(response => response.request().method() === 'POST' && response.url().endsWith('/messages'));
  await page.locator('#builder-message').press('Control+Enter');
  check((await again).status() === 409, 'repeated Send silently removed stale context');
  await page.locator('[data-studio-view="pane"]').click();
  check(await flow().locator('[data-flow-stale]').isVisible(), 'stale selection unexplained');
  await refresh();
  await node(welcomeKey).locator('summary').click();
  await node(welcomeKey).getByRole('button', { name: 'این بخش را با پیکو تغییر بده' }).click();
  check(await page.locator('input[name="selected_revision"]').inputValue() === '4' && !(await page.locator('[data-flow-request-stale]').isVisible()), 'fresh selection did not repair context');
  await page.locator('[data-flow-clear]').click();
  check(await page.locator('input[name="selected_block"]').inputValue() === '', 'explicit clear failed');
  await page.locator('[data-studio-view="pane"]').click();
  await page.emulateMedia({ reducedMotion: 'reduce', colorScheme: 'dark' });
  await page.locator('#studio-tab-preview').click();
  check(await page.locator('[data-studio-preview]').isVisible(), 'Preview tab unavailable');
  await page.locator('#studio-tab-flow').click();
  check(await flow().isVisible(), 'Flow tab unavailable');
  check(errors.length === 0, errors.join('\n'));
  return { screenshots, errors, zoom: true, keyboard: true, handoff: true, committedRefresh: true, staleTargetRejected: true, mobile: true };
}
