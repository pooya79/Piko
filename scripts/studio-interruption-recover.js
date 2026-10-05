// Run after restarting the same temporary server/database; no automatic retry.
async page => {
 const check=(value,reason)=>{if(!value)throw new Error(reason);};
 await page.locator('[data-studio-outcome="interrupted"]').waitFor();
 const providerCalls=async()=>(await(await page.request.get('http://127.0.0.1:18089/call-count')).json()).calls;
 const before=await page.evaluate(()=>Number(sessionStorage.getItem('studio-interruption-calls')));
 await page.evaluate(()=>window.pikoStudio.refresh());
 check(await providerCalls()===before,'restart/reconciliation silently replayed the request');
 check(await page.evaluate(()=>window.interruptionProbe==='intact'),'restart recovery reloaded the page');
 await page.locator('#studio-run-details').evaluate(el=>{el.open=true;});
 await page.locator('form[action$="/retry"] button').click();
 await page.locator('[data-builder-stream-url]').waitFor();
 await page.locator('[data-builder-reply]').filter({hasText:'متن موقت'}).waitFor();
 check(await providerCalls()===before+1,'explicit recovery admitted more than one call');
 check(await page.locator('#builder-message').inputValue()==='پیام من در زمان راه اندازی دوباره','recovery lost unsent input');
 await page.locator('#studio-stop').click();
 await page.locator('[data-studio-outcome="stopped"]').waitFor();
 check(await page.locator('form[action$="/retry"]').count()===0,'stopped recovery still offers retry');
 return { interrupted:true, explicitRecovery:true, noReplay:true, composerPreserved:true };
}
