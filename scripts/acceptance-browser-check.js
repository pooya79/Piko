// Run through playwright-cli against TestAcceptanceBrowserFixture only.
// The fixture has synthetic model/Telegram servers and migrated temporary SQLite.
async page => {
  const origin = page.url().match(/^https?:\/\/[^/]+/)[0];
  const check = (value, message) => { if (!value) throw new Error(message); };
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(origin + '/login');
  await page.locator('#email').fill('deploy-owner@example.test');
  await page.locator('#password').fill('OwnerPassword123');
  await page.locator('form[action="/login"] button').click();
  await page.waitForURL('**/dashboard');

  // The first page keeps its established design; only real entry actions run.
  for (const mobile of [false, true]) {
    await page.setViewportSize(mobile ? { width: 390, height: 844 } : { width: 1440, height: 1000 });
    for (const theme of ['light', 'dark']) {
      await page.locator(`[data-theme-choice="${theme}"]`).click();
      await page.evaluate(() => document.fonts.ready);
      check(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'Dashboard overflows');
      await page.screenshot({ path: `/tmp/piko48/dashboard-${mobile ? 'mobile' : 'desktop'}-${theme}.png`, fullPage: true });
    }
    if (mobile) {
      await page.locator('[data-navigation-open]').focus();
      await page.keyboard.press('Enter');
      check(await page.locator('#workspace-navigation').evaluate(el => el.matches(':modal')), 'mobile navigation is not modal');
      await page.locator('[data-navigation-close]').focus();
      await page.keyboard.press('Shift+Tab');
      check(await page.locator('#workspace-navigation').evaluate(el => el.contains(document.activeElement)), 'drawer let keyboard focus escape');
      await page.keyboard.press('Escape');
      check(await page.locator('[data-navigation-open]').evaluate(el => el === document.activeElement), 'drawer did not return focus');
    }
  }
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.locator('.piko-idea-prompt a').click();
  await page.locator('[data-example="inquiry"]').click();
  check(await page.locator('#builder-message').evaluate(el => el === document.activeElement && el.value.length > 30), 'example cannot be personalized');
  await page.locator('#builder-message').fill('پیکو چه کاری انجام می‌دهد؟');
  await page.locator('#builder-message').press('Control+Enter');
  await page.waitForURL('**/chats/*');
  const originalChat = page.url();
  await page.locator('[data-studio-outcome="succeeded"]').waitFor({ state: 'attached' });
  check(!(await (await page.request.get(origin + '/bots')).text()).includes('piko-bot-card'), 'general question created a Bot');
  await page.evaluate(() => { window.acceptanceCreationProbe = true; });
  await page.locator('#builder-message').fill('ACCEPT_BUILD یک ربات درخواست بساز');
  await page.locator('#builder-message').press('Control+Enter');
  await page.locator('[data-draft-revision="1"]').waitFor();
  await page.locator('[data-builder-stream-url]').waitFor({ state: 'detached' });
  check(await page.evaluate(() => window.acceptanceCreationProbe), 'same-chat creation reloaded the page');
  check((await page.locator('.piko-builder-history').innerText()).includes('پیکو چه کاری انجام می‌دهد؟'), 'creation lost earlier discussion');
  const chatURL = page.url();
  const botPath = chatURL.slice(origin.length).match(/^\/bots\/\d+/)[0];
  const preview = page.locator('[data-studio-preview]');
  const waitPreview = revision => page.waitForFunction(revision => document.querySelector('[data-preview-revision]')?.dataset.previewRevision === revision, String(revision));
  await preview.locator(`form[action="${botPath}/preview"] button`).click();
  await waitPreview(1);
  check(await preview.locator('[data-preview-source-revision="1"]').count() === 1, 'Preview did not identify the newly created Draft');
  await preview.locator('button[name="choice"][value="inquiry"]').click();
  await waitPreview(2);
  await page.locator('#preview-answer').fill('مینا');
  await preview.locator('.piko-preview-answer button').click();
  await waitPreview(3);
  await page.locator('#preview-answer').fill('09123456789');
  await preview.locator('.piko-preview-answer button').click();
  await waitPreview(4);
  await page.locator('#preview-answer').fill('درخواست آزمایشی برای پذیرش');
  await preview.locator('.piko-preview-answer button').click();
  await waitPreview(5);
  await preview.locator('button[name="choice"][value="submit"]').click();
  await waitPreview(6);
  check((await preview.innerText()).includes('درخواست شما دریافت شد'), 'Preview confirmation did not reach the acknowledgement');
  check(!(await (await page.request.get(origin + botPath + '/submissions')).text()).includes('data-submission-id='), 'integrated Preview created a real Submission');
  check(await page.evaluate(() => window.acceptanceCreationProbe), 'integrated Preview reloaded the studio');
  const propose = async action => {
    const count = await page.locator('[data-action-proposal]').count();
    await page.locator('#builder-message').fill(`ACCEPT_${action.toUpperCase()} پیشنهاد ${action}`);
    await page.locator('#builder-message').press('Control+Enter');
    await page.waitForFunction(count => document.querySelectorAll('[data-action-proposal]').length > count, count);
    await page.locator('[data-builder-stream-url]').waitFor({ state: 'detached' });
    return page.locator(`[data-action-proposal][data-action="${action}"]`).last();
  };
  const confirm = async card => {
    const action = await card.locator('form').getAttribute('action');
    if (await card.locator('input[name="operate"]').count()) await card.locator('input[name="operate"]').check();
    await card.locator('button[type="submit"]').focus();
    await page.keyboard.press('Tab');
    await page.keyboard.press('Shift+Tab');
    check(await card.locator('button[type="submit"]').evaluate(el => getComputedStyle(el).outlineStyle !== 'none'), 'confirmation lacks visible keyboard focus');
    await page.keyboard.press('Enter');
    await page.waitForURL(origin + action);
    await page.goto(chatURL);
  };
  let card = await propose('deploy');
  check(await card.locator('form').count() === 0 && await card.locator(`a[href="${botPath}/connect"]`).count() === 1, 'Unconnected proposal lacks credential guidance');
  await card.locator(`a[href="${botPath}/connect"]`).click();
  await page.locator('#token').fill('123456:abcdefghijklmnopqrstuvwxyz0123456789');
  await page.locator(`form[action="${botPath}/connect"] button`).click();
  await page.waitForURL(origin + botPath);
  check(await page.locator('[data-bot-state="inactive"]').count() === 1, 'connection activated delivery');
  await page.goto(chatURL);

  // Changed workspace identity invalidates the specific approval card.
  card = await propose('deploy');
  const staleID = await card.getAttribute('data-action-proposal');
  await page.goto(origin + botPath + '/settings');
  await page.locator('#bot-name').fill('پذیرش Renamed');
  await page.locator(`form[action="${botPath}/name"] button`).click();
  await page.waitForURL('**/settings?saved=1');
  await page.goto(chatURL);
  check(await page.locator(`[data-action-proposal="${staleID}"] form`).count() === 0, 'stale proposal remains actionable');
  card = await propose('deploy');
  check(!(await card.locator('input[name="operate"]').isChecked()), 'deployment is preconfirmed');
  await card.locator('button[type="submit"]').click();
  check(page.url() === chatURL, 'unchecked operating consent submitted');
  await confirm(card);
  await page.goto(origin + botPath);
  check(await page.locator('[data-bot-state="active"]').count() === 1 && (await page.locator('main').innerText()).includes('نسخهٔ منتشرشده: ۱'), 'confirmed deployment did not activate publication one');
  await page.goto(chatURL);
  await confirm(await propose('pause'));
  await page.goto(origin + botPath);
  check(await page.locator('[data-bot-state="paused"]').count() === 1, 'confirmed Pause failed');
  await page.goto(chatURL);
  await confirm(await propose('deploy'));
  await page.goto(origin + botPath);
  check(await page.locator('[data-bot-state="paused"]').count() === 1 && (await page.locator('main').innerText()).includes('نسخهٔ منتشرشده: ۲'), 'Deploy did not preserve pause');
  await page.goto(chatURL);
  await confirm(await propose('resume'));
  await page.goto(origin + botPath);
  check(await page.locator('[data-bot-state="active"]').count() === 1, 'confirmed Resume failed');
  await page.goto(chatURL);
  await page.locator('#builder-message').fill('پیام ناتمام برای ادامه');

  // Operational history is real saved state in every layout and theme.
  for (const mobile of [false, true]) {
    await page.setViewportSize(mobile ? { width: 390, height: 844 } : { width: 1440, height: 1000 });
    for (const theme of ['light', 'dark']) {
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await page.locator(`[data-theme-choice="${theme}"]`).click();
      check(await page.locator('html').getAttribute('dir') === 'rtl', 'missing RTL');
      check(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'confirmation history overflows');
      check(await page.locator('#builder-message').inputValue() === 'پیام ناتمام برای ادامه', 'theme lost unsent text');
      await page.locator('[data-action-proposal]').last().scrollIntoViewIfNeeded();
      await page.screenshot({ path: `/tmp/piko48/confirmations-${mobile ? 'mobile' : 'desktop'}-${theme}.png` });
    }
  }
  check((await page.request.get(originalChat)).ok(), 'converted original chat URL no longer resolves');
  check(errors.length === 0, errors.join('; '));
  return { passed: true, sameChat: true, isolatedPreview: true, staleConfirmation: true, deployPauseResume: true, errors };
}
