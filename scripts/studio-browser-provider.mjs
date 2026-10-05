// Deterministic external-provider fixture; synthetic data and no model calls.
import { createServer } from 'node:http';
const providerCalls = { calls: 0 };
const draft = JSON.stringify({ version: 2, welcome: { id: 'welcome', type: 'message', text: 'سلام از پیش نویس جدید' }, menu: { id: 'menu', type: 'menu', text: 'انتخاب کنید', choices: [{ id: 'collect', label: 'فرم آزمایش', target: 'custom' }] }, messages: [], forms: [{ id: 'custom', review: 'بررسی کنید', acknowledgement: 'دریافت شد', questions: [{ id: 'name', label: 'نام', prompt: 'نام شما؟', type: 'short_text', required: true }] }] });
createServer(async (request, response) => {
  if (request.url === '/call-count') { response.setHeader('Content-Type', 'application/json'); response.end(JSON.stringify(providerCalls)); return; }
  let body = '';
  for await (const chunk of request) body += chunk;
  const input = JSON.parse(body);
  providerCalls.calls++;
  const messages = input.messages || [];
  const latest = [...messages].reverse().find(message => message.role === 'user');
  const text = typeof latest?.content === 'string' ? latest.content : JSON.stringify(latest?.content);
  const last = messages.at(-1);
  const system = messages.filter(message => message.role === 'system').map(message => message.content).join(' ');
  const mode = text?.match(/STUDIO_(BUILD|EDIT|HOLD|FAIL)/)?.[1];
  const reply = content => ({ role: 'assistant', content });
  let message;
  const routing = system.includes('general Piko chat without a Bot');
  if (mode === 'BUILD' && routing) message = reply('{"intent":"build"}');
  else if ((mode === 'BUILD' && system.includes('initial supported build') || mode === 'EDIT') && last?.role !== 'tool') {
    message = { role: 'assistant', tool_calls: [{ id: 'studio-tool', type: 'function', function: { name: mode === 'BUILD' ? 'prepare_bot' : 'prepare_draft', arguments: JSON.stringify(mode === 'BUILD' ? { name: 'ربات آزمایش جریان', definition: draft } : { definition: draft }) } }] };
  } else message = reply('پاسخ ذخیره شده پیکو؛ آماده بررسی است.');
  if (!input.stream) {
    await new Promise(resolve => setTimeout(resolve, 500));
    response.setHeader('Content-Type', 'application/json');
    response.end(JSON.stringify({ choices: [{ message, finish_reason: 'stop' }] }));
    return;
  }
  response.writeHead(200, { 'Content-Type': 'text/event-stream' });
  const frame = (delta, finish_reason = null) => response.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason }] })}\n\n`);
  if (message.tool_calls) {
    frame({ role: 'assistant', tool_calls: message.tool_calls.map((call, index) => ({ ...call, index })) }, 'tool_calls');
    response.end('data: [DONE]\n\n');
    return;
  }
  if (mode === 'BUILD' && routing) { frame(message, 'stop'); response.end('data: [DONE]\n\n'); return; }
  frame(reply('متن موقت <script>provisional</script> '));
  if (mode === 'HOLD') { request.on('close', () => response.end()); return; }
  await new Promise(resolve => setTimeout(resolve, 3000));
  if (response.destroyed) return;
  if (mode === 'FAIL') { response.end('data: [DONE]\n\n'); return; }
  frame({ content: message.content }, 'stop');
  response.end(`data: ${JSON.stringify({ choices: [], usage: { total_tokens: 7 } })}\n\ndata: [DONE]\n\n`);
}).listen(18089, '127.0.0.1');
