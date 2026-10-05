// Local external-provider fixture for studio-browser-check.js; no model calls.
import { createServer } from 'node:http';

createServer(async (request, response) => {
  let body = '';
  for await (const chunk of request) body += chunk;
  const streamed = JSON.parse(body).stream;
  // Leave enough time to edit an unsent follow-up while the run is active.
  await new Promise(resolve => setTimeout(resolve, 3000));
  const choice = {
    index: 0, finish_reason: 'stop',
    [streamed ? 'delta' : 'message']: {
      role: 'assistant', content: 'پیکو قالب دریافت درخواست، ثبت‌نام و درخواست رزرو را می‌سازد.',
    },
  };
  const data = JSON.stringify({ choices: [choice], usage: { total_tokens: 7 } });
  response.writeHead(200, { 'Content-Type': streamed ? 'text/event-stream' : 'application/json' });
  response.end(streamed ? `data: ${data}\n\ndata: [DONE]\n\n` : data);
}).listen(18089, '127.0.0.1');
