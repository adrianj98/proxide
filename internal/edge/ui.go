package edge

// Static HTML for the admin UI. Kept dependency-free (no external assets).

const baseStyle = `<style>
  :root { color-scheme: dark; }
  body { background:#0b0e14; color:#c8d3f5; font-family:ui-monospace,SFMono-Regular,Menlo,monospace; margin:0; padding:0; }
  .wrap { max-width:960px; margin:0 auto; padding:24px; }
  h1 { font-size:18px; font-weight:600; margin:0 0 16px; }
  .muted { color:#7a88b8; font-size:12px; }
  input,textarea,button { font-family:inherit; font-size:14px; border-radius:6px; border:1px solid #2b3550; background:#11151f; color:#c8d3f5; padding:10px; }
  textarea { width:100%; box-sizing:border-box; min-height:64px; resize:vertical; }
  button { background:#3d59a1; border-color:#3d59a1; color:#fff; cursor:pointer; padding:10px 18px; }
  button:hover { background:#4a68b8; }
  pre { background:#06080d; border:1px solid #2b3550; border-radius:6px; padding:14px; min-height:240px; max-height:60vh; overflow:auto; white-space:pre-wrap; word-break:break-word; }
  .row { display:flex; justify-content:space-between; align-items:center; gap:12px; }
  .warn { color:#ffb454; }
  a { color:#73d0ff; }
</style>`

const loginHTML = `<!doctype html><html><head><meta charset="utf-8"><title>devproxy admin</title>` + baseStyle + `</head>
<body><div class="wrap">
  <h1>devproxy admin</h1>
  <p class="muted">Enter the tunnel token to sign in.</p>
  <form method="post" action="/login">
    <input type="password" name="token" placeholder="token" autofocus style="width:100%;box-sizing:border-box;margin-bottom:12px">
    <button type="submit">Sign in</button>
  </form>
</div></body></html>`

const loginErrorHTML = `<!doctype html><html><head><meta charset="utf-8"><title>devproxy admin</title>` + baseStyle + `</head>
<body><div class="wrap">
  <h1>devproxy admin</h1>
  <p class="warn">Invalid token.</p>
  <form method="post" action="/login">
    <input type="password" name="token" placeholder="token" autofocus style="width:100%;box-sizing:border-box;margin-bottom:12px">
    <button type="submit">Sign in</button>
  </form>
</div></body></html>`

const consoleHTML = `<!doctype html><html><head><meta charset="utf-8"><title>devproxy console</title>` + baseStyle + `</head>
<body><div class="wrap">
  <div class="row">
    <h1>devproxy console</h1>
    <form method="post" action="/logout"><button type="submit">Log out</button></form>
  </div>
  <p class="muted">Commands run inside the connected container. <code>cd</code> persists between commands. Press <b>Ctrl/Cmd+Enter</b> to run.</p>
  <div class="muted" style="margin-bottom:6px">cwd: <span id="cwd">…</span></div>
  <textarea id="cmd" placeholder="e.g. cd /var/log && ls -la" autofocus></textarea>
  <div style="margin:12px 0"><button id="run" onclick="run()">Run</button></div>
  <pre id="out"></pre>
</div>
<script>
const cmd = document.getElementById('cmd');
const out = document.getElementById('out');
const btn = document.getElementById('run');
const cwdEl = document.getElementById('cwd');
cmd.addEventListener('keydown', e => {
  if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') { e.preventDefault(); run(); }
});
async function refreshCwd() {
  try { const r = await fetch('/cwd', { headers:{'Accept':'text/plain'} }); if (r.ok) cwdEl.textContent = await r.text(); } catch (e) {}
}
refreshCwd();
async function run() {
  const command = cmd.value;
  if (!command.trim()) return;
  btn.disabled = true;
  out.textContent = '';
  try {
    const res = await fetch('/exec', { method:'POST', headers:{'Accept':'text/plain'}, body: command });
    if (!res.ok) { out.textContent = 'error ' + res.status + ': ' + (await res.text()); return; }
    const reader = res.body.getReader();
    const dec = new TextDecoder();
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      out.textContent += dec.decode(value, { stream:true });
      out.scrollTop = out.scrollHeight;
    }
  } catch (err) {
    out.textContent += '\n[client error: ' + err + ']';
  } finally {
    btn.disabled = false;
    refreshCwd();
  }
}
</script>
</body></html>`
