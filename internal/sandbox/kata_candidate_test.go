package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"testing"

	dockerdefaults "github.com/pacificbelt30/codex-dock/docker"
)

// Reused by candidate qualification inside an already verified real Kata VM.
// The proxy points only to synthetic local upstreams; no host CLI credentials.
func checkCandidateAgentCLIs(t *testing.T, ctx context.Context, manager *Manager, worker string) {
	t.Helper()
	pins := map[string]string{}
	for binary, arg := range map[string]string{"codex": "CODEX_CLI_VERSION", "claude": "CLAUDE_CLI_VERSION"} {
		match := regexp.MustCompile(`(?m)^ARG ` + arg + `=([0-9]+\.[0-9]+\.[0-9]+)$`).FindSubmatch(dockerdefaults.Dockerfile)
		if len(match) != 2 {
			t.Fatal("missing embedded CLI pin", binary)
		}
		pins[binary] = string(match[1])
	}
	input, _ := json.Marshal(pins)
	script := `import os,sys,json,subprocess,re
pins=json.load(sys.stdin)
def run(argv):
 p=subprocess.run(argv,text=True,capture_output=True,stdin=subprocess.DEVNULL,timeout=75)
 assert p.returncode==0,(argv[0],p.returncode,p.stdout,p.stderr)
 return p.stdout
for binary,pin in pins.items():
 version=run([binary,'--version'])
 assert re.search(r'(?<![0-9.])'+re.escape(pin)+r'(?![0-9.])',version),(binary,pin,version)
 print('CANDIDATE_VERSION_'+binary.upper()+'='+pin,flush=True)
provider='model_providers.candidate={name="candidate",base_url='+json.dumps(os.environ['OPENAI_BASE_URL'])+',wire_api="responses",env_key="CODEX_TOKEN",supports_websockets=false}'
output=run(['codex','exec','--skip-git-repo-check','--ephemeral','--json','--model','gpt-5.4-mini','-c','model_provider="candidate"','-c',provider,'Reply with the fixture marker.'])
assert 'CXD_HTTP_SSE_OK' in output,output
print('CANDIDATE_CODEX_SSE_PASS',flush=True)
output=run(['claude','--setting-sources','','-p','Run the fixture command.','--model','claude-sonnet-4-6','--tools','Bash','--allowedTools','Bash','--max-turns','3'])
assert 'CXD_CLAUDE_SSE_OK' in output,output
assert open('cxd-claude-tool.txt').read()=='CXD_CLAUDE_TOOL_OK'
print('CANDIDATE_CLAUDE_TOOL_SSE_PASS',flush=True)`
	var output bytes.Buffer
	code, err := manager.Exec(ctx, worker, []string{"python3", "-c", script}, bytes.NewReader(input), &output, &output)
	if err != nil || code != 0 {
		t.Fatalf("candidate agents: exit=%d err=%v\n%s", code, err, &output)
	}
	t.Log(output.String())
}
