// Run after studio-browser-check.js against the temporary application/provider.
async page => {
  const origin = page.url().match(/^https?:\/\/[^/]+/)[0];
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  const check = (value, reason) => { if (!value) throw new Error(reason); };
  const providerCalls = async () => (await (await page.request.get('http://127.0.0.1:18089/call-count')).json()).calls;
  const send = async text => {
    await page.locator('#builder-message').fill(text);
    await page.locator('#builder-message').press('Control+Enter');
    await page.locator('[data-builder-stream-url]').waitFor();
  };
  const terminal = status => page.locator(`[data-studio-outcome="${status}"]`).waitFor({state:'attached'});
  const fragment = async () => page.evaluate(() => window.pikoStudio.refresh());
  const mark = () => page.evaluate(() => { window.navigationProbe = 'intact'; });
  const intact = async () => check(await page.evaluate(() => window.navigationProbe === 'intact'), 'form caused full-page reload');
  const post = (path, values) => page.evaluate(async ({ path, values }) => {
    const csrf_token = document.querySelector('input[name="csrf_token"]').value;
    const response = await fetch(path, { method: 'POST', body: new URLSearchParams({ csrf_token, ...values }) });
    return response.url;
  }, { path, values });

  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(origin + '/builder');
  await mark();
  await send('STUDIO_BUILD یک ربات ثبت نام بساز');
  const generalPath = page.url().slice(origin.length);
  await page.locator('[data-builder-reply]').filter({ hasText: 'متن موقت' }).waitFor();
  check(await page.locator('[data-draft-revision]').count() === 0, 'provisional initial candidate became a committed Draft');
  check(await page.locator('[data-builder-reply] script').count() === 0, 'provisional text was interpreted as HTML');
  await page.locator('#builder-message').fill('پیام بعدی ناتمام');
  await page.locator('#builder-message').evaluate(el => { el.setSelectionRange(3, 7, 'backward'); });
  await terminal('succeeded');
  await intact();
  const botChat = page.url().slice(origin.length);
  const botPath = botChat.match(/^\/bots\/\d+/)[0];
  check(botChat !== generalPath, 'committed association did not update the URL');
  check(await page.locator('[data-draft-revision]').getAttribute('data-draft-revision') === '1', 'committed identity missing');
  check(await page.locator('#builder-message').inputValue() === 'پیام بعدی ناتمام', 'creation lost unsent text');
  check(await page.locator('#builder-message').evaluate(el => el === document.activeElement && el.selectionStart === 3 && el.selectionEnd === 7 && el.selectionDirection === 'backward'), 'creation lost focus/caret direction');

  // Later pane slices use this adapter boundary for selection/zoom/Preview state.
  await page.evaluate(() => {
    document.querySelector('#studio-pane').dataset.zoom = '1.75';
    document.querySelector('#studio-pane').dataset.selection = 'name';
    window.pikoStudio.registerPaneState('browser-pane', {
      capture: studio => ({ zoom: studio.querySelector('#studio-pane').dataset.zoom, selection: studio.querySelector('#studio-pane').dataset.selection }),
      restore: (studio, state) => Object.assign(studio.querySelector('#studio-pane').dataset, state),
    });
  });
  await send('STUDIO_EDIT پیش نویس را تغییر بده');
  await page.locator('[data-builder-reply]').filter({ hasText: 'متن موقت' }).waitFor();
  check(await page.locator('[data-draft-revision]').getAttribute('data-draft-revision') === '1', 'staged edit changed visible committed revision');
  await page.locator('#builder-message').fill('پیام حفظ شده در نمای کنار');
  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator('[data-studio-view="pane"]').click();
  await page.locator('#studio-preview').focus();
  await terminal('succeeded');
  check(await page.locator('[data-piko-studio]').getAttribute('data-view') === 'pane', 'terminal refresh changed selected mobile pane');
  check(await page.locator('#studio-preview').evaluate(el => el === document.activeElement), 'pane focus lost');
  check(await page.locator('#studio-pane').getAttribute('data-zoom') === '1.75' && await page.locator('#studio-pane').getAttribute('data-selection') === 'name', 'registered pane state lost');
  check(await page.locator('[data-draft-revision]').getAttribute('data-draft-revision') === '2', 'terminal refresh missed committed edit');
  await page.locator('[data-studio-view="conversation"]').click();
  check(await page.locator('#builder-message').inputValue() === 'پیام حفظ شده در نمای کنار', 'mobile pane lost composer');
  await page.setViewportSize({ width: 1440, height: 1000 });

  // Typing during admission must not get cleared with the submitted message.
  await page.route('**/messages', async route => {
    await page.waitForTimeout(500);
    await route.continue();
  }, { times: 1 });
  await page.locator('#builder-message').fill('STUDIO_HOLD منتظر بمان');
  await page.locator('#builder-message').press('Control+Enter');
  await page.locator('#builder-message').fill('در زمان پذیرش تایپ کردم');
  await page.locator('[data-builder-stream-url]').waitFor();
  check(await page.locator('#builder-message').inputValue() === 'در زمان پذیرش تایپ کردم', 'late admission cleared a follow-up');
  await page.locator('#builder-message').evaluate(el => { el.setSelectionRange(2, 4); });
  await page.locator('#studio-stop').click();
  await terminal('stopped');
  await intact();
  check(await page.locator('#builder-message').inputValue() === 'در زمان پذیرش تایپ کردم', 'Stop lost unsent message');
  check(await page.locator('#builder-message').evaluate(el => el === document.activeElement && el.selectionStart === 2 && el.selectionEnd === 4), 'Stop lost composer selection/fallback focus');
  check(await page.locator('form[action$="/retry"]').count() === 0, 'Stop incorrectly offers interrupted retry');

  // Disconnect/reconnect must observe the same request, without re-admission.
  await send('STUDIO_HOLD اتصال را بررسی کن');
  await page.locator('[data-builder-reply]').filter({ hasText: 'متن موقت' }).waitFor();
  const calls = await providerCalls();
  await page.locator('#builder-message').fill('پس از بازگشت ادامه می دهم');
  await page.goto(origin + '/dashboard');
  await page.goto(origin + botChat);
  await page.locator('[data-builder-reply]').filter({ hasText: 'متن موقت' }).waitFor();
  check(await providerCalls() === calls, 'returning replayed provider work');
  check(await page.locator('#builder-message').inputValue() === 'پس از بازگشت ادامه می دهم', 'navigation lost draft composer');
  const stopPath = await page.locator('#studio-stop').evaluate(el => new URL(el.form.action).pathname);
  const secondChat = (await post(botPath + '/chats', { title: 'گفتگوی دیگر مرورگر' })).slice(origin.length);
  await page.goto(origin + secondChat);
  await page.locator('[data-studio-busy]').waitFor();
  check(await page.locator('#studio-active-chat').getAttribute('href') === botChat, 'other-chat work is unidentified');
  check(await page.locator('#studio-send').isDisabled(), 'busy chat composer admits work');
  await page.locator('#builder-message').fill('در گفتگوی دوم منتظر هستم');
  await post(stopPath, {});
  await page.locator('[data-studio-busy]').waitFor({ state: 'detached' });
  check(await page.locator('#studio-send').isEnabled(), 'other-chat completion failed to restore actions');
  check(await page.locator('#builder-message').inputValue() === 'در گفتگوی دوم منتظر هستم', 'busy refresh lost unsent input');

  await send('STUDIO_FAIL پاسخ ناموفق');
  await terminal('failed');
  check(await page.locator('form[action$="/retry"]').count() === 0, 'failure silently broadened retry eligibility');
  await mark();
  await send('پیکو چه امکاناتی دارد؟');
  await page.locator('#builder-message').fill('پیام هنگام قطع جریان');
  // Read-only polling recovers terminal state even with a permanently lost stream.
  await page.evaluate(() => window.dispatchEvent(new Event('pagehide')));
  await terminal('succeeded');
  await intact();
  check(await page.locator('#builder-message').inputValue() === 'پیام هنگام قطع جریان', 'fallback polling lost input');
  const afterCalls = await providerCalls();
  await fragment(); await fragment();
  check(await providerCalls() === afterCalls, 'fragment reconciliation replayed calls');
  check(errors.length === 0, errors.join('\n'));
  return { stagedCreation: true, stagedEdit: true, paneState: true, mobileFocus: true, lateTyping: true, stop: true, disconnectReconnect: true, busy: true, failedRetryRestriction: true, lostStreamRecovery: true, errors };
}
