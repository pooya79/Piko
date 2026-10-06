// Run against TestSubmissionBrowserFixture with PIKO_SUBMISSION_BROWSER_ADDR.
async page => {
  const origin = page.url().match(/^https?:\/\/[^/]+/)[0];
  const check = (ok, message) => { if (!ok) throw new Error(message); };
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.setViewportSize({width:1440,height:1000});
  if (page.url().endsWith('/login')) {
    await page.getByLabel('ایمیل').fill('delivery@example.test');
    await page.locator('#password').fill('OwnerPassword123');
    await page.locator('form[action="/login"] button').click();
    await page.waitForURL('**/dashboard');
  }
  await page.goto(origin+'/bots/1');
  await page.getByRole('link', {name:'دیدن درخواست‌های ثبت‌شده',exact:true}).click();
  await page.waitForURL('**/bots/1/submissions');
  check(await page.locator('[data-submission-id]').count()===50, 'first page must show 50 real records');
  check(await page.locator('.piko-bot-navigation [aria-current="page"]').innerText()==='درخواست‌های دریافت‌شده', 'inbox navigation is not active');
  check(!(await page.locator('main').innerText()).includes('تعامل ناتمام'), 'unfinished Interaction leaked into inbox');
  await page.getByRole('link',{name:'درخواست‌های قدیمی‌تر',exact:true}).click();
  check(await page.locator('[data-submission-id]').count()===1, 'older cursor did not isolate last record');
  await page.getByRole('link',{name:'بازگشت به جدیدترین درخواست‌ها',exact:true}).click();
  await page.locator('[data-submission-id="51"]').getByRole('link',{name:'مشاهده پاسخ‌ها',exact:true}).click();
  check((await page.locator('.piko-submission-answers').innerText()).includes("<script>alert('answer')</script>"), 'escaped literal answer missing');
  check(await page.locator('.piko-submission-answers script').count()===0, 'answer became executable markup');
  check((await page.locator('.piko-submission-context').innerText()).includes('۲'), 'detail lost original published version');
  check(!(await page.locator('.piko-submission-answers').innerText()).includes('نام تازه'), 'new Draft altered frozen labels');
  check(await page.locator('.piko-submission-context bdi').getAttribute('dir')==='ltr', 'Participant identifier not isolated');
  check(await page.locator('a[href$="/delete"]').count() === 0, 'detail still offers deletion');
  const layouts=[{width:1440,height:1000,name:'desktop'},{width:390,height:844,name:'mobile'}];
  for (const layout of layouts) {
    await page.setViewportSize({width:layout.width,height:layout.height});
    for (const theme of ['light','dark','system']) {
      await page.goto(origin+'/bots/1/submissions');
      await page.locator(`[data-theme-choice="${theme}"]`).click();
      for (const [path,name] of [['/bots/1/submissions','inbox'],['/bots/1/submissions/51','detail'],['/bots/2/submissions','empty']]) {
        await page.goto(origin+path);
        check(await page.locator('html').getAttribute('dir')==='rtl', 'missing RTL');
        check(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth), `${layout.name} ${theme} overflow at ${path}`);
        await page.screenshot({path:`/tmp/piko-submissions-${layout.name}-${theme}-${name}.png`,fullPage:name!=='inbox'});
      }
    }
  }
  await page.emulateMedia({reducedMotion:'reduce',colorScheme:'dark'});
  await page.goto(origin+'/bots/1/submissions/51');
  check((await page.goto(origin+'/bots/1/submissions/51/delete')).status()===404, 'removed confirmation still accessible');
  check((await page.goto(origin+'/bots/1/submissions/51')).status()===200, 'record lost');
  await page.goto(origin+'/bots/1/submissions?before=1');
  check((await page.locator('main').innerText()).includes('درخواست قدیمی‌تری برای نمایش وجود ندارد.'), 'empty cursor state misleading');
  await page.screenshot({path:'/tmp/piko-submissions-empty-cursor.png',fullPage:true});
  check((await page.goto(origin+'/bots/1/submissions?before=bad')).status()===400, 'invalid cursor not rejected');
  await page.screenshot({path:'/tmp/piko-submissions-invalid-cursor.png',fullPage:true});
  check(errors.length===0, 'page errors: '+errors.join(', '));
  console.log(JSON.stringify({passed:true,layouts:layouts.map(x=>x.name),themes:['light','dark','system'],errors}));
}
