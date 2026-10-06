// Run against TestFlowBrowserFixture with playwright-cli run-code --filename.
async page => {
  const base = 'http://127.0.0.1:18093';
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  page.on('console', message => { if (message.type() === 'error') errors.push(message.text()); });
  const check = (condition, message) => { if (!condition) throw new Error(message); };
  const settle = () => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  await page.setViewportSize({ width: 1600, height: 1000 });
  await page.goto(base+'/login');
  if (page.url().endsWith('/login')) {
    await page.locator('[name=email]').fill('bot-owner@example.test');
    await page.locator('[name=password]').fill('OwnerPassword123');
    await page.getByRole('button', { name: 'ورود', exact: true }).click();
    await page.waitForURL('**/dashboard');
  }
  await page.goto(base+'/bots/1/flow');
  await page.locator('[data-graph-ready]').waitFor();
  await settle();
  check(!(await page.locator('[data-flow-error]').isVisible()), 'Graph failed to initialize');
  const geometry = await page.evaluate(() => {
    const stage = document.querySelector('.piko-flow-stage').getBoundingClientRect();
    return { width: stage.width, within: [...document.querySelectorAll('[data-flow-key]')].every(node => {
      const box = node.getBoundingClientRect();
      return box.left >= stage.left && box.right <= stage.right && box.top >= stage.top && box.bottom <= stage.bottom;
    }) };
  });
  check(geometry.width > 1100 && geometry.within, 'Fit must show the entire graph in the full workspace');
  check((await page.locator('#flow-open-piko').getAttribute('href')) === '/bots/1/chats/2', 'Piko did not open the most recent Bot chat');
  check(await page.locator('#flow-open-piko').count() === 1 && await page.locator('[data-flow-change]').count() === 0, 'Piko actions repeat on nodes');
  await page.screenshot({ path: '/tmp/piko-flow-browser/desktop.png' });
  const node = page.locator('[data-flow-key]').filter({ hasText: 'نام' }).first();
  await node.click();
  check(await page.locator('[data-flow-inspector]').isVisible(), 'Node click must open the inspector');
  check((await page.locator('[data-flow-detail]:visible .piko-flow-message').textContent()).includes('نام؟'), 'Inspector lost full block text');
  check(await node.getAttribute('aria-pressed') === 'true', 'Selected block is not identified accessibly');
  const transition = page.locator('[data-flow-detail]:visible [data-flow-target]').first();
  check((await transition.locator('.piko-flow-path-condition').textContent()).trim().length > 0, 'Transition needs its trigger');
  check((await transition.locator('.piko-flow-path-body strong').textContent()).trim().length > 0, 'Transition needs its destination name');
  const destination = await transition.getAttribute('data-flow-target');
  await transition.click();
  check(await page.locator('[data-flow-detail]:visible').getAttribute('data-flow-detail') === destination, 'Transition card must inspect its destination');
  await page.locator('.piko-flow-picker summary').click();
  await page.locator('[data-flow-select]').filter({ hasText: 'نام' }).first().click();
  const positions = () => page.locator('[data-flow-key]').evaluateAll(nodes => nodes.map(node => node.style.transform));
  const before = await positions();
  await page.locator('[data-flow-all]').check();
  check(JSON.stringify(before) === JSON.stringify(await positions()), 'Showing secondary connections moved the nodes');
  await page.screenshot({ path: '/tmp/piko-flow-browser/desktop-inspector.png' });
  await page.locator('[data-flow-close]').click();
  check(!(await page.locator('[data-flow-inspector]').isVisible()), 'Inspector did not close');
  await page.locator('.piko-flow-picker summary').click();
  const picker = page.locator('[data-flow-select]').filter({ hasText: 'نام' }).first();
  await picker.focus();
  await page.keyboard.press('Enter');
  check(await page.locator('[data-flow-inspector]').isVisible(), 'Keyboard Block list must open details');
  await page.keyboard.press('Escape');
  check(!(await page.locator('[data-flow-inspector]').isVisible()), 'Escape must close the inspector');
  await page.locator('[data-flow-zoom=in]').click();
  await page.locator('[data-flow-fit]').click();
  await page.locator('[data-flow-key]').filter({ hasText: 'نام' }).first().click();
  const minimap = await page.locator('[data-flow-minimap]').boundingBox();
  const unpanned = await positions();
  await page.mouse.click(minimap.x+12, minimap.y+minimap.height/2);
  check(await page.locator('[data-flow-inspector]').isVisible(), 'Panning must preserve the selected Block');
  const camera = await positions();
  check(JSON.stringify(camera) !== JSON.stringify(unpanned), 'Minimap did not pan the graph');

  await page.goto(base+'/bots/1/flow?view=published');
  await page.locator('[data-graph-ready]').waitFor();
  check(await page.locator('[data-flow-page]').getAttribute('data-flow-revision') === '1', 'Published view followed Draft edits');
  await page.goto(base+'/bots/3/flow');
  await page.locator('[data-graph-ready]').waitFor();
  check(await page.locator('[data-flow-key]').count() === 86, 'Maximum approved Flow did not render all Blocks');
  await page.locator('[data-flow-all]').check();
  await page.screenshot({ path: '/tmp/piko-flow-browser/maximum-flow.png' });
  const nojsContext = await page.context().browser().newContext({ javaScriptEnabled: false, storageState: await page.context().storageState() });
  const nojs = await nojsContext.newPage();
  await nojs.goto(base+'/bots/3/flow');
  await nojs.locator('.piko-flow-fallback details').evaluateAll(nodes => nodes.forEach(node => { node.open = true; }));
  const lastFallback = nojs.locator('.piko-flow-fallback details').last();
  await lastFallback.scrollIntoViewIfNeeded();
  check(await lastFallback.evaluate(node => node.getBoundingClientRect().bottom <= innerHeight), 'No-script inspection clips the final Block');
  await nojsContext.close();
  await page.goto(base+'/bots/2/flow?view=published');
  check(await page.locator('.piko-flow-empty').isVisible(), 'Unpublished Flow needs an empty state');

  const mobileContext = await page.context().browser().newContext({
    viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true,
    storageState: await page.context().storageState(),
  });
  const mobile = await mobileContext.newPage();
  await mobile.goto(base+'/bots/1/flow');
  await mobile.locator('[data-graph-ready]').waitFor();
  await mobile.waitForFunction(() => document.querySelector('[data-flow-key]').getBoundingClientRect().height >= 44);
  await mobile.locator('.piko-flow-picker summary').tap();
  await mobile.locator('[data-flow-select]').filter({ hasText: 'نام' }).first().tap();
  const sheet = await mobile.locator('[data-flow-inspector]').boundingBox();
  check(sheet.width === 390 && Math.abs(sheet.y+sheet.height-844) < 2 && sheet.height <= 465, 'Mobile inspector must be a bottom sheet');
  const visibleNode = await mobile.locator('[data-flow-key][aria-pressed=true]').boundingBox();
  check(visibleNode.y+visibleNode.height < sheet.y, 'Mobile inspection must keep the selected Block visible above the sheet');
  check(await mobile.evaluate(() => document.documentElement.scrollWidth <= 390), 'Mobile Flow overflows horizontally');
  await mobile.screenshot({ path: '/tmp/piko-flow-browser/mobile-inspector.png' });
  await mobile.locator('[data-flow-close]').tap();
  await mobile.locator('[data-flow-fit]').tap();
  await mobile.screenshot({ path: '/tmp/piko-flow-browser/mobile.png' });
  await mobile.getByRole('link', { name: 'آزمایش پیش‌نویس', exact: true }).tap();
  await mobile.locator('[data-studio-preview]').waitFor({ state: 'visible' });
  check(await mobile.locator('[data-studio-preview]').isVisible(), 'Test draft must open Preview on mobile');
  await mobileContext.close();

  await page.goto(base+'/bots/1/flow');
  await page.getByRole('button', { name: 'تیره', exact: true }).click();
  await page.screenshot({ path: '/tmp/piko-flow-browser/dark.png' });
  await page.getByRole('button', { name: 'روشن', exact: true }).click();
  check(errors.length === 0, 'Browser errors or CSP violations: '+errors.join('; '));
  return 'Passed: full graph, inspector, keyboard, stable layout, Published isolation, 86-node Flow, no-script scrolling, mobile sheet, Preview shortcut, dark theme, CSP';
}
