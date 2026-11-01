import {chromium} from 'playwright';
import {readFileSync,mkdirSync} from 'node:fs';
import {spawn} from 'node:child_process';
const report=JSON.parse(readFileSync('.live-report.local','utf8'));
const server=spawn('npm',['run','dev:react','--','--port','4174'],{detached:true,stdio:'ignore'});
let browser;
try{
 for(let n=0;n<40;n++){try{if((await fetch('http://127.0.0.1:4174')).ok)break}catch{}await new Promise(r=>setTimeout(r,250));}
 browser=await chromium.launch();const page=await browser.newPage({viewport:{width:1440,height:900}});
 const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(`http://127.0.0.1:4174/video/${report.video_id}`);
 await page.waitForFunction(()=>{const v=document.querySelector('video');return v&&v.currentTime>.1&&!v.paused},{},{timeout:30000});
 mkdirSync('test-results',{recursive:true});await page.screenshot({path:'test-results/live-desktop.png',fullPage:true});
 await page.setViewportSize({width:390,height:844});await page.screenshot({path:'test-results/live-mobile.png',fullPage:true});
 if(errors.length)throw Error(errors.join('\n'));console.log('PASS: React detail fetch, actual server media playback and desktop/mobile screenshots.');
}finally{await browser?.close();try{process.kill(-server.pid,'SIGTERM')}catch{}}
