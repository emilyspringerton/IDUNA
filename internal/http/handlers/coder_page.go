// coder_page.go — /admin/coder: chat with the coder agent inside the IDUNA admin.
// The page talks to the cookie-auth twin of the coder API (/admin/coder/api/...); all rendering
// uses textContent so agent/tool output can never inject markup.
package handlers

import "net/http"

type CoderPageHandler struct{}

func (CoderPageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin/coder" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'")
	w.Write([]byte(coderPageHTML))
}

const coderPageHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Coder Agent</title>
<style>
body{font:14px system-ui,sans-serif;margin:0;background:#101014;color:#e6e6ea;display:flex;height:100vh}
#side{width:260px;border-right:1px solid #2a2a33;padding:12px;overflow:auto}
#main{flex:1;display:flex;flex-direction:column;min-width:0}
#log{flex:1;overflow:auto;padding:12px}
.m{margin:6px 0;padding:8px 10px;border-radius:6px;white-space:pre-wrap;word-break:break-word}
.user{background:#1e2a22}.assistant{background:#1a1a20}.tool{background:#14141a;color:#9aa;font:12px ui-monospace,monospace}
#bar{display:flex;gap:8px;padding:10px;border-top:1px solid #2a2a33}
textarea{flex:1;background:#17171c;color:inherit;border:1px solid #2a2a33;border-radius:6px;padding:8px;font:inherit}
button{background:#7ee8a2;color:#101014;border:0;border-radius:6px;padding:8px 14px;font-weight:600;cursor:pointer}
button.s{background:#2a2a33;color:#e6e6ea;font-weight:400;padding:4px 8px}
.sess{padding:6px;border-radius:4px;cursor:pointer}.sess:hover,.sess.on{background:#1e1e26}
h4{margin:14px 0 6px;color:#71717a;font-weight:600}#err{color:#f87171;padding:4px 12px}
@media(max-width:700px){body{flex-direction:column}#side{width:auto;max-height:30vh;border-right:0;border-bottom:1px solid #2a2a33}}
</style></head><body>
<div id="side"><a href="/admin" style="color:#7ee8a2">&larr; admin</a>
<h4>Sessions</h4><button class="s" id="new">+ new</button><div id="sessions"></div>
<h4>Worker</h4><button class="s" id="mint">mint worker agent</button><pre id="cred" style="white-space:pre-wrap;word-break:break-all;font-size:11px"></pre>
<h4>Lessons <span style="font-weight:400">(feedback loop)</span></h4><div id="lessons"></div>
<input id="lt" placeholder="teach it something" style="width:100%;margin-top:6px;box-sizing:border-box"><button class="s" id="la">add lesson</button>
</div>
<div id="main"><div id="err"></div><div id="log"></div>
<div id="bar"><textarea id="in" rows="2" placeholder="Talk to the coder agent... (Enter to send, Shift+Enter for newline)"></textarea><button id="send">Send</button></div></div>
<script>
const API='/admin/coder/api';let sid=null,last=0,timer=null;
const $=id=>document.getElementById(id);
function err(e){$('err').textContent=e||''}
async function j(path,opt){const r=await fetch(API+path,Object.assign({headers:{'Content-Type':'application/json'}},opt));
 const t=await r.text();let d={};try{d=JSON.parse(t)}catch(e){}
 if(!r.ok)throw new Error((d.error||r.status)+(d.required_permission?' (needs '+d.required_permission+')':''));return d}
function add(m){const d=document.createElement('div');d.className='m '+m.role;
 d.textContent=(m.role==='tool'?'['+m.tool_name+'] ':'')+m.content;$('log').appendChild(d);$('log').scrollTop=1e9}
async function poll(){if(!sid)return;try{const d=await j('/sessions/'+sid+'/messages?after='+last);
 d.messages.forEach(m=>{last=Math.max(last,m.id);add(m)});err('')}catch(e){err(e.message)}}
async function open(id){sid=id;last=0;$('log').textContent='';clearInterval(timer);await poll();timer=setInterval(poll,2000);sessions()}
async function sessions(){try{const d=await j('/sessions');const el=$('sessions');el.textContent='';
 d.sessions.forEach(s=>{const x=document.createElement('div');x.className='sess'+(s.id===sid?' on':'');
 x.textContent=(s.title||s.id)+' · '+s.created_at.slice(5,16).replace('T',' ');x.onclick=()=>open(s.id);el.appendChild(x)})}catch(e){err(e.message)}}
async function lessons(){try{const d=await j('/lessons');const el=$('lessons');el.textContent='';
 d.lessons.forEach(l=>{const x=document.createElement('div');x.textContent='• '+l.text+' ';
 const b=document.createElement('button');b.className='s';b.textContent='x';b.onclick=async()=>{await j('/lessons/'+l.id,{method:'DELETE'});lessons()};x.appendChild(b);el.appendChild(x)})}catch(e){err(e.message)}}
$('new').onclick=async()=>{try{const s=await j('/sessions',{method:'POST',body:JSON.stringify({title:'chat '+new Date().toISOString().slice(5,16)})});await open(s.id)}catch(e){err(e.message)}};
async function send(){const c=$('in').value.trim();if(!c)return;try{if(!sid){await $('new').onclick()}
 await j('/sessions/'+sid+'/messages',{method:'POST',body:JSON.stringify({content:c})});$('in').value='';poll()}catch(e){err(e.message)}}
$('send').onclick=send;$('in').addEventListener('keydown',e=>{if(e.key==='Enter'&&!e.shiftKey&&!e.isComposing){e.preventDefault();send()}});
$('la').onclick=async()=>{try{await j('/lessons',{method:'POST',body:JSON.stringify({text:$('lt').value})});$('lt').value='';lessons()}catch(e){err(e.message)}};
$('mint').onclick=async()=>{if(!confirm('Mint a new worker agent? Its secret is shown once.'))return;
 try{const d=await j('/workers',{method:'POST',body:'{}'});
 $('cred').textContent='IDUNA_AGENT_NAME='+d.agent_name+'\nIDUNA_AGENT_SECRET='+d.agent_secret+'\n# permissions: '+d.permissions.join(', ')+'\n# shown once - copy it now';err('')}catch(e){err(e.message)}};
sessions();lessons();
</script></body></html>`
