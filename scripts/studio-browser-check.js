// Run against an isolated development server with a local test provider:
// playwright-cli run-code --filename=/absolute/path/scripts/studio-browser-check.js
// Browser output stays in /tmp; never use a production database or provider key.
async page => {
  const origin = page.url().match(/^https?:\/\/[^/]+/)[0];
  const errors = [];
  await page.route('**/static/**', route => route.continue());
  page.on('pageerror', error => errors.push(error.message));
  const check = (condition, message) => { if (!condition) throw new Error(message); };
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(origin + '/dashboard');
  if (page.url().endsWith('/login')) {
    await page.goto(origin + '/register');
    await page.locator('#display-name').fill('آزمایش فضای پیکو');
    await page.locator('#email').fill(`studio-${Date.now()}@example.test`);
    await page.locator('#password').fill('StudioBrowser123');
    await page.locator('form[action="/register"] button').click();
    await page.waitForURL('**/dashboard');
  }
  await page.locator('.piko-idea-prompt a').click();
  await page.locator('[data-piko-studio]').waitFor();
  check(await page.locator('input[name="name"], input[name="title"], input[name="token"]').count() === 0, 'entry asks for setup');
  let posts = 0;
  const recordPost = request => { if (request.method() === 'POST') posts++; };
  page.on('request', recordPost);
  for (const kind of ['inquiry', 'registration', 'booking']) {
    await page.locator(`[data-example="${kind}"]`).click();
    check((await page.locator('#builder-message').inputValue()).length > 30, 'example did not fill composer');
    check(await page.locator('#builder-message').evaluate(el => el === document.activeElement), 'example did not focus composer');
  }
  check(posts === 0, 'selecting an example submitted work');
  page.off('request', recordPost);
  await page.locator('#builder-message').fill('پیکو چه امکاناتی دارد؟');
  await page.locator('#builder-message').press('Control+Enter');
  await page.waitForURL('**/chats/*');
  const chat = page.url().slice(origin.length);
  await page.locator('[data-builder-stream-url]').waitFor();
  await page.locator('#builder-message').fill('پیام بعدی که هنوز نفرستاده‌ام');
  await page.locator('#builder-message').press('ArrowLeft');
  const selection = await page.locator('#builder-message').evaluate(el => el.selectionStart);
  await page.locator('[data-run-status="succeeded"]').waitFor({ state: 'attached' });
  check(await page.locator('#builder-message').inputValue() === 'پیام بعدی که هنوز نفرستاده‌ام', 'terminal update lost composer');
  check(await page.locator('#builder-message').evaluate(el => el === document.activeElement && el.selectionStart) === selection, 'terminal update lost focus or selection');
  check(await page.locator('.piko-builder-history').innerText().then(text => text.includes('پیکو چه امکاناتی دارد؟')), 'history missing first turn');
  const completeWithFocus = async (control, openDetails) => {
    await page.locator('#builder-message').fill('پیکو چه امکاناتی دارد؟');
    await page.locator('#builder-message').press('Control+Enter');
    await page.locator('[data-builder-stream-url]').waitFor();
    if (openDetails) await page.locator(openDetails).evaluate(el => { el.open = true; });
    await page.locator(control).focus();
    await page.locator('[data-builder-stream-url]').waitFor({ state: 'detached' });
    const expected = control === '#studio-stop' ? '#builder-message' : control;
    check(await page.locator(expected).evaluate(el => el === document.activeElement), `terminal update lost ${control} focus`);
    if (openDetails) check(await page.locator(openDetails).evaluate(el => el.open), 'terminal update closed details');
  };
  await completeWithFocus('#studio-chat-picker', '#studio-chat-selector');
  await page.keyboard.press('Escape');
  await completeWithFocus('#studio-stop');
  await page.setViewportSize({ width: 390, height: 844 });
  const refresh = () => page.evaluate(() => window.pikoStudio.refresh());
  const history = page.locator('.piko-studio-scroll');
  check(await history.evaluate(el => el.scrollHeight > el.clientHeight + 50), 'fixture needs scrollable history');
  await history.evaluate(el => { el.scrollTop = 50; });
  await page.locator('[data-studio-view="pane"]').click();
  await refresh();
  await page.locator('[data-studio-view="conversation"]').click();
  check(await history.evaluate(el => el.scrollTop) === 50, 'completion in mobile pane lost conversation reading position');
  await history.focus();
  await refresh();
  check(await history.evaluate(el => el === document.activeElement), 'completion lost history keyboard focus');
  await page.setViewportSize({ width: 1440, height: 1000 });

  // Named manual setup remains a supported server operation; the UI entry is Piko.
  await page.evaluate(async () => {
    const csrf = document.querySelector('input[name="csrf_token"]').value;
    await fetch('/bots/new', { method: 'POST', body: new URLSearchParams({ csrf_token: csrf, name: 'ربات آزمایش مرورگر' }) });
  });
  // Discover the actual created Bot; this also makes the script rerunnable.
  await page.goto(origin + '/bots');
  const botURL = await page.locator('.piko-bot-card a[href^="/bots/"]').first().getAttribute('href');
  const botPath = botURL.match(/^\/bots\/\d+/)[0];
  await page.evaluate(async botPath => {
    const csrf = document.querySelector('input[name="csrf_token"]').value;
    const response = await fetch(botPath + '/chats', { method: 'POST', body: new URLSearchParams({ csrf_token: csrf, title: 'گفت‌وگوی ربات مرورگر' }) });
    location.href = response.url;
  }, botPath);
  await page.locator('.piko-studio-chat-picker').waitFor();
  const botChat = page.url().slice(origin.length);
  await page.locator('.piko-studio-chat-picker summary').click();
  check(await page.locator(`.piko-studio-saved a[href="${chat}"]`).count() === 1, 'general chat undiscoverable');
  await page.locator(`.piko-studio-saved a[href="${chat}"]`).click();
  await page.waitForURL(origin + chat);
  await page.locator('.piko-studio-chat-picker summary').click();
  check((await page.locator(`.piko-studio-saved a[href="${botChat}"]`).innerText()).includes('ربات آزمایش مرورگر'), 'Bot association missing');
  await page.locator(`.piko-studio-saved a[href="${botChat}"]`).click();
  await page.waitForURL(origin + botChat);
  check(await page.locator(`#studio-pane a[href="${botPath}/draft"]`).count() === 0, 'removed editor is linked');
  check(await page.locator(`[data-studio-preview] form[action="${botPath}/preview"] button`).count() === 1, 'embedded Preview launch missing');
  check((await page.request.get(origin + botPath + '/preview')).ok(), 'standalone Preview unavailable');
  await completeWithFocus('#studio-preview');

  const results = [];
  for (const size of ['desktop', 'mobile']) {
    await page.setViewportSize(size === 'desktop' ? { width: 1440, height: 1000 } : { width: 390, height: 844 });
    for (const theme of ['light', 'dark']) {
      await page.locator(`[data-theme-choice="${theme}"]`).click();
      await page.evaluate(() => document.fonts.ready);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth);
      check(!overflow, `${size}/${theme} horizontal overflow`);
      await page.screenshot({ path: `/tmp/piko-studio-${size}-${theme}.png` });
      results.push({ size, theme, overflow });
    }
    if (size === 'mobile') {
      await page.locator('#builder-message').fill('پیام موبایل ناتمام');
      await page.locator('[data-studio-view="pane"]').focus();
      await page.keyboard.press('Enter');
      check(await page.locator('#studio-pane').isVisible(), 'mobile pane not full view');
      check(!(await page.locator('#studio-conversation').isVisible()), 'mobile conversation overlaps pane');
      await page.screenshot({ path: '/tmp/piko-studio-mobile-pane-dark.png' });
      await page.locator('[data-studio-view="conversation"]').focus();
      await page.keyboard.press('Enter');
      check(await page.locator('#builder-message').inputValue() === 'پیام موبایل ناتمام', 'pane switching lost text');
    }
  }
  // Long history scrolls independently and keeps the complete composer visible.
  await page.evaluate(() => {
    const list = document.querySelector('.piko-builder-history') || document.querySelector('.piko-studio-scroll');
    for (let i = 0; i < 30; i++) {
      const p = document.createElement('p');
      p.textContent = 'متن طولانی برای بررسی پیمایش گفت‌وگو';
      list.append(p);
    }
  });
  const bounds = await page.locator('.piko-studio-composer').boundingBox();
  check(bounds.y >= 0 && bounds.y + bounds.height <= 845, 'composer left viewport');
  await page.emulateMedia({ colorScheme: 'dark', reducedMotion: 'reduce' });
  await page.locator('[data-theme-choice="system"]').click();
  check(await page.locator('html').getAttribute('data-theme') === 'piko-dark', 'system theme ignored');
  check(await page.locator('.piko-studio').evaluate(el => getComputedStyle(el).scrollBehavior) === 'auto', 'reduced motion ignored');
  check(errors.length === 0, errors.join('\n'));
  return { results, errors, directEntry: true, examples: true, savedChats: true, persistentComposer: true, keyboard: true, mobileSwitching: true };
}
