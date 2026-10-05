// Leave this browser open, restart the temporary server, then run recovery.
async page => {
 const origin=page.url().match(/^https?:\/\/[^/]+/)[0];
 await page.goto(origin+'/builder');
 await page.locator('#builder-message').fill('STUDIO_HOLD درخواست قابل بازیابی');
 await page.locator('#builder-message').press('Control+Enter');
 await page.locator('[data-builder-stream-url]').waitFor();
 await page.locator('[data-builder-reply]').filter({hasText:'متن موقت'}).waitFor();
 const calls=(await (await page.request.get('http://127.0.0.1:18089/call-count')).json()).calls;
 await page.locator('#builder-message').fill('پیام من در زمان راه اندازی دوباره');
 if(await page.locator('#builder-message').inputValue()!=='پیام من در زمان راه اندازی دوباره') throw new Error('restart fixture did not enter the complete unsent message');
 await page.evaluate(calls=>{sessionStorage.setItem('studio-interruption-calls',String(calls));window.interruptionProbe='intact';},calls);
 return { readyForRestart:true, chat:page.url(), calls };
}
