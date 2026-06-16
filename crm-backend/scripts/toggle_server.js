// Toggleable webhook target for audit #7. Returns 500 until /flip is hit, then 200.
const http = require('http');
let mode = 500;
const server = http.createServer((req, res) => {
  if (req.url === '/flip') { mode = mode === 500 ? 200 : 500; res.writeHead(200); res.end('mode=' + mode); console.log('flipped to', mode); return; }
  if (req.url === '/status') { res.writeHead(200); res.end('mode=' + mode); return; }
  // consume body
  let b=[]; req.on('data',c=>b.push(c)); req.on('end',()=>{
    res.writeHead(mode, { 'Content-Type':'application/json' });
    res.end(JSON.stringify({ ok: mode===200, mode }));
    console.log(new Date().toISOString(), req.method, req.url, '->', mode);
  });
});
server.listen(9099, () => console.log('toggle server on :9099, mode=500'));
