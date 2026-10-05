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

  await page.locator('.piko-settings-panel a[href="/bots/1/draft"]').click();
  check(await page.locator('input[name="form_id"]').count() === 3, 'manual settings lost supported Forms');
  const firstForm = page.locator('.piko-form-settings').first();
  const firstQuestion = firstForm.locator('.piko-question-settings').first();
  await firstQuestion.locator('summary').click();
  await firstQuestion.locator('textarea[name="question_prompt"]').fill('');
  await firstQuestion.locator('summary').click();
  await firstForm.locator('summary').first().click();
  await page.locator('#draft-save').click();
  check(await firstQuestion.getAttribute('open') !== null && await firstForm.getAttribute('open') !== null, 'native validation did not reveal collapsed field');
  check(await firstQuestion.locator('textarea[name="question_prompt"]').evaluate(el => el === document.activeElement), 'native validation did not focus question');
  await firstQuestion.locator('textarea[name="question_prompt"]').fill('تاریخ دلخواه شما؟');
  await page.locator('#welcome').fill('سلام از تنظیمات تازه');
  const stale = await page.context().newPage();
  await stale.goto(origin + '/bots/1/draft');
  await page.locator('#draft-save').click();
  await page.waitForURL('**/draft?saved=1');
  await stale.locator('#welcome').fill('نسخهٔ قدیمی ذخیره نشود');
  await stale.locator('#draft-save').click();
  await stale.locator('#draft-error').waitFor();
  check(await stale.locator('#welcome').inputValue() === 'نسخهٔ قدیمی ذخیره نشود', 'conflict discarded candidate');
  await stale.close();
  await page.goto(origin + '/bots/1/draft');
  check(await page.locator('#welcome').inputValue() === 'سلام از تنظیمات تازه', 'stale save overwrote current Draft');

  const layouts = [{width:1440,height:1000,name:'desktop'},{width:390,height:844,name:'mobile'}];
  for (const layout of layouts) {
    await page.setViewportSize({width:layout.width,height:layout.height});
    for (const theme of ['light','dark','system']) {
      await page.goto(origin + '/bots/1/settings');
      await page.locator(`[data-theme-choice="${theme}"]`).click();
      for (const [path,name] of [['/bots/1/settings','settings'],['/bots/1/draft','manual'],['/bots/1/settings/delete','delete']]) {
        await page.goto(origin + path);
        if (name === 'manual') {
          await page.locator('.piko-form-settings').evaluateAll(els => els.forEach(el => { el.open=true; }));
          await page.locator('.piko-question-settings').evaluateAll(els => els.forEach(el => { el.open=true; }));
          await page.locator('textarea[name="question_prompt"]').last().focus();
          await page.waitForFunction(() => {
            const field=document.activeElement.getBoundingClientRect(), bar=document.querySelector('.piko-draft-savebar').getBoundingClientRect();
            return field.top >= 0 && field.bottom <= bar.top;
          });
          check(await page.locator('textarea[name="question_prompt"]').last().evaluate(el => {
            const field=el.getBoundingClientRect(), bar=document.querySelector('.piko-draft-savebar').getBoundingClientRect();
            return field.top >= 0 && field.bottom <= bar.top;
          }), `${layout.name} ${theme} save bar obscures focused field`);
        }
        check(await page.locator('html').getAttribute('dir') === 'rtl', 'missing RTL');
        check(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${layout.name} ${theme} overflow at ${path}`);
        await page.screenshot({path:`/tmp/piko-settings-${layout.name}-${theme}-${name}.png`,fullPage:name!=='manual'});
        if (name==='manual' && layout.name==='desktop' && theme==='light') {
          await page.screenshot({path:'/tmp/piko-settings-long-form.png',fullPage:true});
        }
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
  await page.goto(origin + '/bots/1/draft');
  check(await page.locator('#welcome').inputValue()==='سلام از تنظیمات تازه', 'chat deletion reverted Draft');
  await page.goto(origin + '/bots/1/settings/delete');
  await confirmation.check();
  await page.locator('form[action="/bots/1/delete"] button').click();
  await page.waitForURL('**/bots');
  for (const path of ['/bots/1/chats/2','/bots/1/draft']) {
    check((await page.goto(origin+path)).status()===404, 'Bot deletion retained owned data');
  }
  for (const path of ['/chats/3','/bots/2']) {
    check((await page.goto(origin+path)).status()===200, 'Bot deletion removed unrelated work');
  }
  check(errors.length===0, 'page errors: '+errors.join(', '));
  console.log(JSON.stringify({passed:true,layouts:layouts.map(x=>x.name),themes:['light','dark','system'],errors}));
}
