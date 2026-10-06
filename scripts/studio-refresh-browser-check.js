// Run with the same isolated app/provider; tests presentation failures and IME.
async page => {
 const origin=page.url().match(/^https?:\/\/[^/]+/)[0];
 await page.route('**/static/**',route=>route.continue());
 const check=(value,reason)=>{if(!value)throw new Error(reason);};
 await page.goto(origin+'/builder');
 await page.locator('#builder-message').fill('پیکو چه امکاناتی دارد؟');
 await page.locator('#builder-message').press('Control+Enter');
 await page.locator('[data-builder-stream-url]').waitFor();
 await page.locator('#builder-message').fill('متن در حال ترکیب');
 await page.locator('#builder-message').evaluate(el=>el.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true})));
 await page.evaluate(()=>{
  window.imeRefreshResolved=false;
  window.imeRefresh=window.pikoStudio.refresh().then(()=>{window.imeRefreshResolved=true;});
 });
 await page.waitForFunction(async()=>{
  const url=document.querySelector('[data-piko-studio]').dataset.chatUrl;
  return (await(await fetch(url+'/status')).json()).status==='succeeded';
 });
 check(await page.evaluate(()=>!window.imeRefreshResolved),'refresh API resolved before IME reconciliation');
 check(await page.locator('[data-builder-stream-url]').count()===1,'terminal refresh interrupted IME composition');
 await page.locator('#builder-message').evaluate(el=>el.dispatchEvent(new CompositionEvent('compositionend',{bubbles:true})));
 await page.evaluate(()=>window.imeRefresh);
 await page.locator('[data-studio-outcome="succeeded"]').waitFor({ state: 'attached' });
 check(await page.locator('#builder-message').inputValue()==='متن در حال ترکیب','IME text lost');
 // A composition that starts after GET dispatch also holds the refresh promise.
 await page.route('**/chats/*',async route=>{
  if(route.request().method()==='GET' && route.request().headers()['x-piko-studio']==='fragment') await page.waitForTimeout(500);
  await route.continue();
 },{times:1});
 const dispatched=page.waitForRequest(request=>request.method()==='GET' && request.headers()['x-piko-studio']==='fragment');
 await page.evaluate(()=>{window.imeRefreshResolved=false;window.imeRefresh=window.pikoStudio.refresh().then(()=>{window.imeRefreshResolved=true;});});
 await dispatched;
 await page.locator('#builder-message').evaluate(el=>el.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true})));
 await page.waitForTimeout(600);
 check(await page.evaluate(()=>!window.imeRefreshResolved),'in-flight refresh resolved before IME reconciliation');
 await page.locator('#builder-message').evaluate(el=>el.dispatchEvent(new CompositionEvent('compositionend',{bubbles:true})));
 await page.evaluate(()=>window.imeRefresh);


 await page.locator('#builder-message').fill('پرسش دوم درباره پیکو');
 await page.locator('#builder-message').press('Control+Enter');
 await page.locator('[data-builder-stream-url]').waitFor();
 await page.locator('#builder-message').fill('پیام در زمان خطای نمایش');
 await page.route('**/chats/*',route=>{
  if(route.request().method()==='GET' && route.request().headers()['x-piko-studio']==='fragment') return route.abort();
  return route.continue();
 });
 await page.locator('[data-studio-refresh-error]').waitFor();
 check(await page.locator('#builder-message').inputValue()==='پیام در زمان خطای نمایش','refresh error lost text');
 const calls=(await(await page.request.get('http://127.0.0.1:18089/call-count')).json()).calls;
 await page.unroute('**/chats/*');
 await page.locator('[data-studio-refresh]').click();
 await page.locator('[data-studio-outcome="succeeded"]').waitFor({ state: 'attached' });
 check((await(await page.request.get('http://127.0.0.1:18089/call-count')).json()).calls===calls,'refresh recovery replayed provider work');
 check(await page.locator('#builder-message').inputValue()==='پیام در زمان خطای نمایش','refresh recovery lost text');
 await page.addInitScript(()=>{window.EventSource=undefined;});
 await page.goto(origin+'/builder');
 await page.locator('#builder-message').fill('پرسش بدون پشتیبانی جریان');
 await page.locator('#builder-message').press('Control+Enter');
 await page.locator('[data-builder-stream-url]').waitFor();
 await page.locator('#builder-message').fill('پیام بدون EventSource');
 await page.locator('[data-studio-outcome="succeeded"]').waitFor({ state: 'attached' });
 check(await page.locator('#builder-message').inputValue()==='پیام بدون EventSource','polling without EventSource lost text');
 return { imePreserved:true, refreshFailureRecoverable:true, noAutomaticReplay:true, withoutEventSource:true };
}
