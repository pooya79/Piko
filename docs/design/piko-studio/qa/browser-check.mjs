import {chromium} from 'playwright';
import fs from 'node:fs/promises';
const browser=await chromium.launch({executablePath:'/usr/bin/google-chrome',headless:true,args:['--no-sandbox']});
const errors=[],results=[];
const page=await browser.newPage({viewport:{width:1440,height:1000},deviceScaleFactor:1,colorScheme:'light'});
page.on('pageerror',e=>errors.push(e.message));
async function shot(label){await page.waitForTimeout(700);await page.evaluate(()=>document.fonts.ready);await page.screenshot({path:`qa/${label}.png`,fullPage:true});results.push({view:label,overflow:await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),brokenImages:await page.locator('img').evaluateAll(imgs=>imgs.filter(i=>!i.complete||!i.naturalWidth).map(i=>i.src))});}
async function route(id){await page.goto(`http://localhost:4174/#${id}`);await page.waitForLoadState('networkidle');}
await route('home');await shot('home-desktop');if(await page.locator('.rt-SelectTrigger').evaluate(e=>e.getBoundingClientRect().height)<25)throw Error('Radix sizing tokens missing');
const lightColor=await page.locator('.main-shell').evaluate(e=>getComputedStyle(e.closest('.piko-theme')).backgroundColor);await page.getByRole('button',{name:'حالت تاریک',exact:true}).click();await shot('home-dark');const darkColor=await page.locator('.main-shell').evaluate(e=>getComputedStyle(e.closest('.piko-theme')).backgroundColor);if(lightColor===darkColor)throw Error('Dark theme did not change rendered colors');await page.getByRole('button',{name:'حالت روشن',exact:true}).click();
await route('bots');await shot('bots-desktop');
await page.getByRole('textbox',{name:'جست‌وجوی ربات'}).fill('پیدا نمیشه');await page.getByRole('heading',{name:'رباتی با این نام پیدا نشد'}).waitFor();
await page.getByRole('button',{name:'پاک کردن جست‌وجو'}).click();
await page.getByRole('button',{name:'مدیریت ربات'}).first().click();await shot('bot-detail');
await page.getByRole('button',{name:'جریان ربات',exact:true}).click();await shot('bot-flow');
await page.getByRole('button',{name:'میز خالی داریم؟ بررسی ظرفیت زمان انتخاب‌شده'}).click();await page.getByText('ظرفیت باقی‌مانده با تعداد مهمان‌ها مقایسه می‌شود.').waitFor();
await page.getByRole('button',{name:'آمار ربات',exact:true}).click();await shot('bot-analytics');
await route('chat');await shot('chat-desktop');
await page.getByRole('button',{name:'رزرو میز',exact:true}).click();await page.getByRole('button',{name:'امروز',exact:true}).click();await page.getByText('همه میزها پر شدن.').waitFor();
await page.getByRole('textbox',{name:'پیام به پیکو'}).fill('ظرفیت میز را تغییر بده');await page.getByRole('button',{name:'ارسال پیام'}).click();await page.getByText('پیکو در حال بررسی ایده توست...').waitFor();await page.getByText('متوجه شدم. این تغییر رو به طرح ربات').waitFor();
await page.getByRole('button',{name:'انتشار تغییرات'}).click();await page.getByRole('button',{name:'نسخه جدید منتشر شد'}).waitFor();
await route('analytics');await shot('analytics-desktop');
await route('billing');await shot('billing-desktop');await page.getByRole('button',{name:'مدیریت اشتراک'}).click();await page.getByRole('dialog').waitFor();await page.getByRole('button',{name:'متوجه شدم'}).click();
await route('create');await shot('create-desktop');await page.getByRole('button',{name:'توکن رو گرفتم'}).click();await page.getByRole('button',{name:'ادامه',exact:true}).click();await page.getByText('نام ربات و یک توکن نمونه').waitFor();await page.getByRole('textbox',{name:'نام ربات',exact:true}).fill('نمونه');await page.locator('#bottoken').fill('sample-token');await page.getByRole('button',{name:'ادامه',exact:true}).click();await page.getByRole('button',{name:'ثبت سفارش',exact:true}).click();if(!(await page.getByRole('textbox',{name:'توضیح ایده ربات'}).inputValue()).includes('ثبت سفارش'))throw Error('Suggestion did not fill input');await shot('create-idea');
await page.setViewportSize({width:390,height:844});
for(const id of ['home','bots','analytics','billing','create','chat']){await route(id);await shot(`${id}-mobile`)}
await page.getByRole('button',{name:'پیش‌نمایش',exact:true}).click();await shot('chat-preview-mobile');await page.getByRole('button',{name:'بازگشت به گفت‌وگو'}).click();
await page.getByRole('button',{name:'باز کردن منو'}).click();await page.getByRole('button',{name:'حالت تاریک',exact:true}).click();await page.getByRole('button',{name:'خانه',exact:true}).click();await shot('home-mobile-dark');
await page.getByRole('button',{name:'باز کردن منو'}).click();await page.getByRole('button',{name:'کافه لیمو',exact:true}).click();await page.getByRole('button',{name:'جریان ربات',exact:true}).click();await shot('flow-mobile-dark');
await fs.writeFile('qa/browser-results.json',JSON.stringify({errors,results},null,2));console.log(JSON.stringify({errors,results}));
await browser.close();
