const https = require('https');

const VERCEL_URL = 'https://ai-gateway.vercel.sh/v1/messages';
const KEY = process.env.VERCEL_AI_GATEWAY_KEY || 'MISSING_API_KEY';

function makeAIRequest() {
  return new Promise((resolve, reject) => {
    // Generate a long system prompt (>1024 tokens) to guarantee cache creation
    const longSystemText = "You are a highly capable AI assistant for the CRM. ".repeat(400);

    const payload = JSON.stringify({
      model: "anthropic/claude-haiku-4.5",
      max_tokens: 500,
      system: [
        { 
          type: "text", 
          text: longSystemText, 
          cache_control: { type: "ephemeral" } 
        }
      ],
      messages: [{ role: "user", content: "What is 2+2?" }]
    });

    const url = new URL(VERCEL_URL);
    const opts = {
      hostname: url.hostname,
      port: 443,
      path: url.pathname,
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Content-Length': Buffer.byteLength(payload),
        'Authorization': `Bearer ${KEY}`, // Fallback for some wrappers
        'x-api-key': KEY,
        'anthropic-version': '2023-06-01'
      }
    };

    const req = https.request(opts, res => {
      let chunks = [];
      res.on('data', c => chunks.push(c));
      res.on('end', () => {
        try {
          const body = JSON.parse(Buffer.concat(chunks).toString());
          resolve(body);
        } catch (e) {
          resolve(Buffer.concat(chunks).toString());
        }
      });
    });
    req.on('error', reject);
    req.write(payload);
    req.end();
  });
}

async function run() {
  console.log("Making 3 sequential calls to Vercel AI Gateway (Anthropic cache logic).");
  console.log("Sending a huge system prompt to ensure caching engages...\n");
  
  for (let i = 1; i <= 3; i++) {
    const start = Date.now();
    const res = await makeAIRequest();
    
    if (res.usage) {
      console.log(`--- Call #${i} (${Date.now() - start}ms) ---`);
      console.log(JSON.stringify(res.usage, null, 2));
    } else {
      console.log(`--- Call #${i} FAILED ---`);
      console.log(res);
      break;
    }
  }
}

run();
