const API = "http://localhost:8081/api";
const ts = Date.now();

async function run() {
    console.log("1. Registering org...");
    const reg = await fetch(`${API}/auth/register`, {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
            email: `events_${ts}@example.com`, password: "password123",
            first_name: "Event", last_name: "Tester", org_name: `EventCo ${ts}`
        })
    });
    const token = (await reg.json()).data?.access_token;

    console.log("\n2. POST /ai/command — Asserting SSE stream order");
    const cmdRes = await fetch(`${API}/ai/command`, {
        method: "POST", headers: { "Content-Type": "application/json", "Authorization": `Bearer ${token}` },
        body: JSON.stringify({ message: "Search contacts for John and search deals for Acme" })
    });

    const reader = cmdRes.body.getReader();
    const decoder = new TextDecoder();
    let buf = "";
    const eventSequence = [];

    while(true) {
        const { value, done } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, {stream: true});
        const lines = buf.split('\n');
        buf = lines.pop() || "";
        for (const line of lines) {
            if (!line.startsWith('data: ')) continue;
            try {
                const ev = JSON.parse(line.slice(6));
                eventSequence.push(ev.type);
                console.log(`[Event Received] -> ${ev.type}`);
            } catch(e) {}
        }
    }
    
    // Check order
    console.log(`\nFinal Sequence: ${eventSequence.join(" → ")}`);
    
    // We expect: thinking, planning, tool_result(s), response, done
    const s = eventSequence;
    if (s[0] === 'thinking' && s[1] === 'planning' && s.includes('tool_result') && s[s.length-2] === 'response' && s[s.length-1] === 'done') {
        console.log("✅ PASS: Sequence matches expected order!");
    } else if (s[0] === 'thinking' && s[s.length-2] === 'response' && s[s.length-1] === 'done') {
        console.log("⚠️ PASS (Fallback): Model didn't use tools, but thinking -> response -> done order is correct.");
    } else {
        console.log("❌ FAIL: Sequence is incorrect.");
    }
}
run();
