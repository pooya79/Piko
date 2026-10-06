// Run against TestConnectionBrowserFixture with PIKO_SETTINGS_BROWSER_ADDR.
async page => {
  const origin = page.url().match(/^https?:\/\/[^/]+/)[0];
  const check = (ok, message) => { if (!ok) throw new Error(message); };
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.setViewportSize({ width:1440, height:1000 });
  if (page.url().endsWith('/login')) {
    await page.locator('#email').fill('bot-owner@example.test');
    await page.locator('#password').fill('OwnerPassword123');
    await page.locator('form[action="/login"] button').click();
    await page.waitForURL('**/dashboard');
  }
  await page.goto(origin + '/bots/1/settings');
  await page.locator('#bot-name').fill('    ');
  await page.locator('form[action="/bots/1/name"] button').click();
  await page.locator('#bot-name-error').waitFor();
  check(await page.locator('#bot-name').evaluate(el => el === document.activeElement), 'name error lost focus');
  check(await page.locator('#bot-name').inputValue() === '    ', 'name error discarded input');
  await page.locator('#bot-name').fill('فضای کاری Example');
  await page.keyboard.press('Tab');
  check(await page.locator('form[action="/bots/1/name"] button').evaluate(el => el === document.activeElement), 'keyboard cannot reach name save');
  await page.keyboard.press('Enter');
  await page.waitForURL('**/settings?saved=1');
  check(await page.locator('[role="status"]').count() > 0, 'name save has no feedback');
  check(await page.locator('input[name="token"]').count() === 0, 'settings requests credentials');

  check(await page.locator('a[href="/bots/1/draft"]').count() === 0, 'settings links to removed editor');
  for (const method of ['GET', 'POST']) {
    const response = await page.request.fetch(origin + '/bots/1/draft', { method, form: { csrf_token: await page.locator('[name="csrf_token"]').first().inputValue() } });
    check(response.status() === 404, 'removed draft editor route is available');
  }

  const layouts = [{width:1440,height:1000,name:'desktop'},{width:390,height:844,name:'mobile'}];
  for (const layout of layouts) {
    await page.setViewportSize({width:layout.width,height:layout.height});
    for (const theme of ['light','dark','system']) {
      await page.goto(origin + '/bots/1/settings');
      await page.locator(`[data-theme-choice="${theme}"]`).click();
      for (const [path,name] of [['/bots/1/settings','settings'],['/bots/1/settings/delete','delete']]) {
        await page.goto(origin + path);
        check(await page.locator('html').getAttribute('dir') === 'rtl', 'missing RTL');
        check(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${layout.name} ${theme} overflow at ${path}`);
        await page.screenshot({path:`/tmp/piko-settings-${layout.name}-${theme}-${name}.png`,fullPage:true});
      }
    }
  }
  await page.emulateMedia({reducedMotion:'reduce',colorScheme:'dark'});
  await page.goto(origin + '/bots/1/settings/delete');
  const confirmation = page.locator('#confirm-delete');
  check(!(await confirmation.isChecked()), 'Bot delete is preconfirmed');
  await confirmation.focus();
  await page.keyboard.press('Tab');
  check(await page.locator('form[action="/bots/1/delete"] button').evaluate(el => el === document.activeElement), 'keyboard cannot reach delete confirmation');
  await page.locator('form[action="/bots/1/delete"] button').click();
  check(page.url().endsWith('/settings/delete'), 'unchecked Bot deletion submitted');
  await page.locator('form[action="/bots/1/delete"] a').click();
  await page.waitForURL('**/bots/1/settings');
  await page.goto(origin + '/bots/1/chats/1');
  await page.locator('#studio-delete-summary-1').click();
  check((await page.locator('#studio-delete-1').innerText()).includes('تغییرات ذخیره‌شده'), 'chat confirmation omits retained Draft');
  await page.locator('#studio-delete-confirm-1').click();
  await page.waitForURL('**/bots/1/chats');
  await page.goto(origin + '/bots/1/flow');
  check(await page.locator('[data-flow-page]').getAttribute('data-flow-revision') === '2', 'chat deletion changed Draft revision');
  await page.goto(origin + '/bots/1/settings/delete');
  await confirmation.check();
  await page.locator('form[action="/bots/1/delete"] button').click();
  await page.waitForURL('**/bots');
  for (const path of ['/bots/1/chats/2','/bots/1/flow']) {
    check((await page.goto(origin+path)).status()===404, 'Bot deletion retained owned data');
  }
  for (const path of ['/chats/3','/bots/2']) {
    check((await page.goto(origin+path)).status()===200, 'Bot deletion removed unrelated work');
  }
  check(errors.length===0, 'page errors: '+errors.join(', '));
  console.log(JSON.stringify({passed:true,layouts:layouts.map(x=>x.name),themes:['light','dark','system'],errors}));
}
