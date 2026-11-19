// Ramp test: how many requests a second the cluster serves before it gets
// slow or starts failing. One target per run (TARGET), requests at a fixed
// arrival rate that steps up every STEP (default 1 min), so the latency at
// each rate is in the summary. It stops by
// itself once more than 5% fail or p95 goes over 2 s, so the public sites
// it shares the cluster with don't go down with it.
//
//   BASE   where to send the requests (Kong's in-cluster address, or the
//          VPS nginx), the Host header picks the site
//   TARGET web | bundle | api | nodes
//   RATES  comma-separated requests per second, one step each
import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.BASE || 'http://kong-gateway-proxy.kong.svc';
const TARGET = __ENV.TARGET || 'web';
const STEP = __ENV.STEP || '1m';
const RATES = (__ENV.RATES || '25,50,100,200,300,400,600').split(',').map(Number);

const targets = {
  web: { host: 'status.smega.eu', path: '/' },
  bundle: { host: 'status.smega.eu', path: __ENV.BUNDLE || '/assets/index.js' },
  api: { host: 'api-status.smega.eu', path: '/api/status' },
  nodes: { host: 'api-status.smega.eu', path: '/api/nodes' },
};
const t = targets[TARGET];

// One constant-rate scenario per step, one after the other, each tagged
// with its rate: the thresholds on the tagged sub-metrics make the summary
// carry latency and errors per step, not only for the whole run.
const sec = (d) => (d.endsWith('m') ? parseFloat(d) * 60 : parseFloat(d));
const scenarios = {};
const thresholds = {
  http_req_failed: [{ threshold: 'rate<0.05', abortOnFail: true, delayAbortEval: '20s' }],
  http_req_duration: [{ threshold: 'p(95)<2000', abortOnFail: true, delayAbortEval: '20s' }],
};
RATES.forEach((r, i) => {
  scenarios[`r${r}`] = {
    executor: 'constant-arrival-rate',
    rate: r,
    timeUnit: '1s',
    duration: STEP,
    startTime: `${i * sec(STEP)}s`,
    preAllocatedVUs: Math.min(50 + r, 400),
    maxVUs: 600,
    tags: { rate: String(r) },
  };
  thresholds[`http_req_duration{rate:${r}}`] = ['max>=0'];
  thresholds[`http_req_failed{rate:${r}}`] = ['rate>=0'];
  thresholds[`http_reqs{rate:${r}}`] = ['count>=0'];
  thresholds[`dropped_iterations{rate:${r}}`] = ['count>=0'];
});

export const options = {
  insecureSkipTLSVerify: true,
  discardResponseBodies: true,
  scenarios,
  thresholds,
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

export default function () {
  const res = http.get(`${BASE}${t.path}`, { headers: { Host: t.host }, timeout: '10s', tags: { target: TARGET } });
  check(res, { '2xx': (r) => r.status >= 200 && r.status < 300 });
}

// The whole summary as JSON on stdout between markers, so it survives the
// Job's pod (kubectl logs) and can be cut out by script (run.sh).
export function handleSummary(data) {
  return { stdout: `\n=====K6-SUMMARY=====\n${JSON.stringify(data)}\n=====END=====\n` };
}
