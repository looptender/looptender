// Run only against server.py's fictional, POST-refusing fixture.
const p=await browser.getPage('main');
const origin='http://127.0.0.1:60298';
const checks=[];
function check(name,ok){checks.push({name,ok});if(!ok)throw new Error(name);}
await p.goto(origin+'/freshness/#/accounts');await p.waitForSelector('.account-card');
check('fictional fixture',await p.$eval('body',e=>e.dataset.fictionalQuotaFixture==='true'));
await p.click('#pause');
await p.$eval('body',()=>{window.accountsFixtureNow=Date.parse('2030-03-04T12:00:00Z');Date.now=()=>window.accountsFixtureNow;document.dispatchEvent(new Event('visibilitychange'));});
check('remaining and countdown visible while collapsed',await p.$eval('.quota-compact',e=>e.textContent.includes('62.5% remaining')&&e.textContent.includes('Resets in 1 h')&&e.textContent.includes('Saved snapshot')));
await p.click('.account-card summary');
check('local reset timestamp and exact timestamp title',await p.$eval('.account-quota',e=>e.textContent.includes('(local time)')&&e.querySelector('.quota-countdown').title.includes('2030-03-04T13:00:00Z')));
check('active exhausted window explicitly labelled',await p.$eval('#pane',e=>{const c=Array.from(e.querySelectorAll('.account-card')).find(c=>c.textContent.includes('Example Claude'));return c.textContent.includes('Quota exhausted in saved reading')&&c.textContent.includes('0% remaining');}));
check('initial three live percentage meters',await p.$eval('.account-quota',e=>e.querySelectorAll('meter:not([hidden])').length===3));
await p.$eval('.account-card summary',e=>e.focus());
await p.$eval('body',()=>{window.accountsFixtureNow+=3600000;});
// Exercise the real 15-second timer while saved reads are paused.
await p.waitForFunction(()=>document.querySelector('.quota-countdown').textContent.includes('passed'),{timeout:20000});
check('passed reset retains history and hides meter without refill',await p.$eval('.account-quota',e=>e.querySelector('.quota-window').textContent.includes('Window ended · Last reading: 37.5% used')&&e.querySelectorAll('meter:not([hidden])').length===2));
check('old snapshot advances to stale with reads paused',await p.$eval('.quota-stamp',e=>e.textContent.includes('1 h ago')&&e.textContent.includes('Older reading')));
check('timer preserves focus and disclosure',await p.$eval('.account-card details',e=>e.open&&document.activeElement===e.querySelector('summary')));
await p.$eval('body',()=>{window.accountsFixtureNow+=86400000;document.dispatchEvent(new Event('visibilitychange'));});
check('all passed percentage windows hidden but reset-unknown zero stays',await p.$eval('.account-quota',e=>e.querySelectorAll('meter:not([hidden])').length===1&&e.textContent.includes('Reset time unavailable')));
await p.$eval('#pane',e=>e.querySelectorAll('details').forEach(d=>d.open=true));
check('exhausted history explicit and never replenished',await p.$eval('#pane',e=>{const c=Array.from(e.querySelectorAll('.account-card')).find(c=>c.textContent.includes('Example older'));return c.textContent.includes('Quota exhausted in saved reading')&&c.textContent.includes('Refresh failed')&&!c.querySelector('meter:not([hidden])');}));
check('retrieval remains provider freshness unknown after time advances',await p.$eval('#pane',e=>{const c=Array.from(e.querySelectorAll('.account-card')).find(c=>c.textContent.includes('Example Claude'));return c.textContent.includes('Provider freshness unknown')&&!c.textContent.includes('Saved snapshot');}));
check('login age advances and asks to confirm after 24 hours',await p.$eval('.account-age',e=>e.textContent.includes('25 h ago')&&e.textContent.includes('Older check')&&e.textContent.includes('check again to confirm')));
await p.goto(origin+'/owner/#/accounts');await p.waitForSelector('.account-card');await p.click('#pause');
await p.$eval('body',()=>{Date.now=()=>Date.parse('2030-03-04T12:00:00Z');document.dispatchEvent(new Event('visibilitychange'));});
check('missing quota never creates zero or implies login',await p.$eval('#pane',e=>{const c=Array.from(e.querySelectorAll('.account-card')).find(c=>c.textContent.includes('Example Claude'));return c.querySelector('.quota-compact').textContent.includes('Account quota unavailable')&&!c.querySelector('.quota-compact button')&&!c.querySelector('.account-quota meter');}));
check('partial evidence preserves unavailable values',await p.$eval('#pane',e=>{const c=Array.from(e.querySelectorAll('.account-card')).find(c=>c.textContent.includes('Example partial'));return c.textContent.includes('Missing values are not zero')&&!c.querySelector('meter');}));
for(const theme of ['light','dark']){await p.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);for(const width of [320,390,760,768,1280,1920]){await p.setViewport({width,height:1000});check(theme+' '+width+' no overflow',await p.$eval('body',()=>document.documentElement.scrollWidth<=innerWidth));}}
await p.setViewport({width:390,height:1000});await p.emulateMediaFeatures([{name:'prefers-color-scheme',value:'light'}]);await p.$eval('.account-quota',e=>e.scrollIntoView());await p.shot({type:'jpeg',quality:75,maxEdge:1000});
await p.setViewport({width:1440,height:1000});await p.emulateMediaFeatures([{name:'prefers-color-scheme',value:'dark'}]);await p.$eval('#pane',e=>{e.querySelectorAll('details').forEach(d=>d.open=false);e.scrollIntoView();});await p.shot({type:'jpeg',quality:75,maxEdge:1200});
check('no quota refresh or login action submitted',await p.$eval('body',()=>!performance.getEntriesByType('resource').some(r=>/\/(prepare|confirm|run)$/.test(r.name))));
console.log(JSON.stringify(checks));
