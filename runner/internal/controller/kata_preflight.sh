set -u
echo "[devfleet] preflight: pwd=$(pwd)"
echo "[devfleet] preflight: workspace"
ls -la /workspace || true
echo "[devfleet] preflight: resolv.conf"
cat /etc/resolv.conf || true
echo "[devfleet] preflight: proxy=$DEVFLEET_PROXY"
curl -sS --connect-timeout 5 -x "http://$DEVFLEET_PROXY" -o /dev/null -w "[devfleet] preflight: synthetic http=%{http_code}\n" https://api.synthetic.new/openai/v1/models || echo "[devfleet] preflight: synthetic proxy failed"
curl --noproxy '*' -sS --connect-timeout 5 -o /dev/null -w "[devfleet] preflight: synthetic direct http=%{http_code}\n" https://api.synthetic.new/openai/v1/models || echo "[devfleet] preflight: synthetic direct failed"
curl --noproxy '*' -sS --connect-timeout 5 --max-time 30 -o /tmp/devfleet-chat.json -w "[devfleet] preflight: chat http=%{http_code}\n" -H "Authorization: Bearer $SYNTHETIC_API_KEY" -H "Content-Type: application/json" -d '{"model":"hf:zai-org/GLM-5.1","messages":[{"role":"user","content":"Reply with OK only."}],"max_tokens":16}' https://api.synthetic.new/openai/v1/chat/completions || echo "[devfleet] preflight: chat failed"
test -s /tmp/devfleet-chat.json && head -c 500 /tmp/devfleet-chat.json && echo || true
echo "[devfleet] preflight: opencode version"
opencode --version 2>&1 || echo "[devfleet] preflight: opencode version failed"
echo "[devfleet] preflight: TCP connectivity (bash) to api.synthetic.new:443"
timeout 10 bash -c 'exec 3<>/dev/tcp/api.synthetic.new/443 2>/dev/null && echo BASH_TCP_OK || echo BASH_TCP_FAIL' 2>&1 || echo "[devfleet] preflight: bash tcp timeout=$?"
echo "[devfleet] preflight: IPv4-only curl test"
curl -4 --noproxy '*' -sS --connect-timeout 5 -o /dev/null -w "[devfleet] preflight: curl4 http=%{http_code}\n" https://api.synthetic.new/openai/v1/models || echo "[devfleet] preflight: curl4 failed"
echo "[devfleet] preflight: IPv6-only curl test"
curl -6 --noproxy '*' -sS --connect-timeout 5 -o /dev/null -w "[devfleet] preflight: curl6 http=%{http_code}\n" https://api.synthetic.new/openai/v1/models || echo "[devfleet] preflight: curl6 failed (expected in Kata)"
echo "[devfleet] preflight: exec opencode from /tmp (tmpfs) with --dir /workspace (timeout 300s + strace)"
cd /tmp && strace -f -e trace=network,read,write,process,select,poll,epoll_wait,connect,accept,getpeername -tt -y -s 128 -o /host-tmp/strace.log timeout 300 $DEVFLEET_CMD < /dev/null || echo '[devfleet] preflight: opencode timed out or exited non-zero'
echo "[devfleet] preflight: strace summary:"
grep -c "+++ exited" /host-tmp/strace.log 2>/dev/null || echo "no strace log"
tail -n 5 /host-tmp/strace.log 2>/dev/null || true
