// Run only against TestConnectionBrowserFixture; all tokens and data are synthetic.
async page => {
  const origin = page.url().match(/^https?:\/\/[^/]+/)[0];
  const check = (condition, message) => { if (!condition) throw new Error(message); };
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(origin + '/dashboard');
  if (page.url().endsWith('/login')) {
    await page.locator('#email').fill('bot-owner@example.test');
    await page.locator('#password').fill('OwnerPassword123');
    await page.locator('form[action="/login"] button').click();
    await page.waitForURL('**/dashboard');
  }
  await page.goto(origin + '/bots/1/chats/1');
  await page.locator('#builder-message').fill('پیام نفرستاده محفوظ');
  await page.locator('#studio-pane a[href="/bots/1/connect"]').click();
  const token = page.locator('#token');
  check(await token.getAttribute('type') === 'password' && await token.getAttribute('dir') === 'ltr', 'initial secret field is not masked LTR');
  check(await page.locator('html').getAttribute('dir') === 'rtl', 'missing RTL');
  await token.fill('invalid-synthetic-token');
  await page.locator('form[action="/bots/1/connect"] button').click();
  await page.locator('#token-error').waitFor();
  check(await token.inputValue() === '' && await token.getAttribute('aria-invalid') === 'true', 'initial error retained secret or lost field error');
  await token.fill('123456:abcdefghijklmnopqrstuvwxyz0123456789');
  await page.locator('form[action="/bots/1/connect"] button').click();
  await page.waitForURL(origin + '/bots/1');
  check(await page.locator('[data-bot-state="inactive"]').count() === 1, 'connection activated delivery');
  await page.goto(origin + '/bots/1/connection');
  check(await page.locator('a[href="/bots/1#bot-deployment"]').count() === 1, 'unpublished connection lacks deployment guidance');
  await page.locator('a[href="/bots/1/studio"]').last().click();
  await page.waitForURL('**/bots/1/chats/1');
  check(await page.locator('#builder-message').inputValue() === 'پیام نفرستاده محفوظ', 'connection lost studio composer');
  await page.goto(origin + '/bots/1/connection');
  await page.locator('#lifecycle-token').fill('999999:abcdefghijklmnopqrstuvwxyz0123456789');
  await page.locator('form[action="/bots/1/replace-token"] button').click();
  await page.locator('#lifecycle-token-error').waitFor();
  check(await page.locator('#lifecycle-token').inputValue() === '', 'replacement error retained secret');
  check(await page.locator('#lifecycle-token').getAttribute('aria-invalid') === 'true', 'identity error not bound to field');
  check(!(await page.content()).includes('999999:abcdefghijklmnopqrstuvwxyz0123456789'), 'replacement exposed secret');
  const layouts = [{ width:1440,height:1000,name:'desktop' },{ width:390,height:844,name:'mobile' }];
  for (const layout of layouts) {
    await page.setViewportSize({ width:layout.width, height:layout.height });
    for (const theme of ['light','dark','system']) {
      await page.locator(`[data-theme-choice="${theme}"]`).click();
      for (const path of ['/bots/1/connection','/bots/1/connection/disconnect']) {
        await page.goto(origin + path);
        check(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${layout.name} ${theme} overflow at ${path}`);
        check(await page.locator('h1').count() === 1, 'missing focused title');
        await page.screenshot({ path:`/tmp/piko-connection-${layout.name}-${theme}-${path.endsWith('disconnect') ? 'disconnect':'token'}.png`, fullPage:true });
      }
    }
  }
  await page.emulateMedia({ reducedMotion:'reduce', colorScheme:'dark' });
  await page.goto(origin + '/bots/1/connection');
  await page.locator('#lifecycle-token').focus();
  await page.keyboard.press('Tab');
  check(await page.locator('form[action="/bots/1/replace-token"] button').evaluate(el => el === document.activeElement), 'keyboard cannot reach token submit');
  await page.locator('#lifecycle-token').fill('123456:abcdefghijklmnopqrstuvwxyz0123456789');
  await page.locator('form[action="/bots/1/replace-token"] button').click();
  await page.waitForURL(origin + '/bots/1');
  await page.goto(origin + '/bots/1/connection/disconnect');
  check(await page.locator('input[name="token"]').count() === 0, 'disconnect asks for credentials');
  await page.locator('a[href="/bots/1/connection"]').last().click();
  check(await page.locator('form[action="/bots/1/replace-token"]').count() === 1, 'cancel changed connection');
  await page.goto(origin + '/bots/1/connection/disconnect');
  await page.locator('form[action="/bots/1/disconnect"] button').click();
  await page.waitForURL(origin + '/bots/1');
  check(await page.locator('[data-bot-state="disconnected"]').count() === 1, 'disconnect state not distinct');
  await page.goto(origin + '/bots/1/connection');
  check(await page.locator('form[action="/bots/1/reconnect"]').count() === 1, 'disconnected Bot lacks focused reconnect');
  check((await page.locator('.piko-connection-identity').innerText()).includes('123456'), 'disconnection lost identity');
  await page.locator('#lifecycle-token').fill('123456:abcdefghijklmnopqrstuvwxyz0123456789');
  await page.locator('form[action="/bots/1/reconnect"] button').click();
  await page.waitForURL(origin + '/bots/1');
  check(await page.locator('[data-bot-state="inactive"]').count() === 1, 'reconnection activated delivery');
  await page.goto(origin + '/bots/1/chats/1');
  check(await page.locator('#builder-message').inputValue() === 'پیام نفرستاده محفوظ', 'lifecycle lost studio work');
  check(errors.length === 0, 'browser errors: ' + errors.join(', '));
  console.log(JSON.stringify({ passed:true,layouts:layouts.map(x=>x.name),themes:['light','dark','system'],errors }));
}
