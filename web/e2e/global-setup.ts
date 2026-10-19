// Starts one fresh jec server per Playwright project and hands each test
// its setup token through the environment, with a directory the servers
// may deploy files to (JEC_E2E_FILES). JEC_E2E_BIN names the binary,
// which must embed the current build of the UI:
//
//	pnpm build && go build -o ../build/jec ../cmd/jec
//	JEC_E2E_BIN=../build/jec pnpm e2e
import { spawn, type ChildProcess } from 'node:child_process'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const servers = [
  { project: 'phone', port: 18090 },
  { project: 'desktop', port: 18091 },
]

// A plugin (docs/development/plugins.md) for each server, where shell
// scripts run: it offers one deploy target, and answers any other request
// with success.
const plugin = `#!/bin/sh
cat > /dev/null
cat <<'EOF'
{"protocol": 1, "ok": true, "name": "E2E plugin", "description": "A plugin of the end-to-end tests",
 "types": [{"id": "cdn", "kind": "target", "testable": true, "name": "E2E plugin target", "description": "Deploys nowhere",
  "fields": [{"key": "endpoint", "kind": "url", "label": "Endpoint", "help": "Where it {would} deploy", "required": true}]}]}
EOF
`

function installPlugin(dir: string): void {
  if (process.platform === 'win32') return
  mkdirSync(join(dir, 'plugins'), { mode: 0o755 })
  writeFileSync(join(dir, 'plugins', 'e2e'), plugin, { mode: 0o755 })
}

function start(bin: string, port: number, dir: string): Promise<{ proc: ChildProcess; token: string }> {
  return new Promise((resolve, reject) => {
    const proc = spawn(bin, ['serve', '--data-dir', dir, '--listen', `127.0.0.1:${port}`], { stdio: ['ignore', 'ignore', 'pipe'] })
    let log = ''
    const timer = setTimeout(() => reject(new Error(`jec on port ${port} logged no setup token:\n${log}`)), 30_000)
    proc.stderr?.on('data', (chunk: Buffer) => {
      log += chunk.toString()
      const token = /setup_token=(\w+)/.exec(log)?.[1]
      if (token && log.includes('msg=listening')) {
        clearTimeout(timer)
        resolve({ proc, token })
      }
    })
    proc.on('exit', (code) => {
      clearTimeout(timer)
      reject(new Error(`jec on port ${port} exited with ${code}:\n${log}`))
    })
  })
}

export default async function globalSetup(): Promise<() => void> {
  const bin = process.env.JEC_E2E_BIN
  if (!bin) throw new Error('set JEC_E2E_BIN to a jec binary built with the current UI')
  const running: { proc: ChildProcess; dir: string }[] = []
  const files = mkdtempSync(join(tmpdir(), 'jec-e2e-files-'))
  process.env.JEC_E2E_FILES = files
  for (const { project, port } of servers) {
    const dir = mkdtempSync(join(tmpdir(), 'jec-e2e-'))
    installPlugin(dir)
    const { proc, token } = await start(bin, port, dir)
    process.env[`JEC_E2E_TOKEN_${project.toUpperCase()}`] = token
    running.push({ proc, dir })
  }
  return () => {
    for (const { proc, dir } of running) {
      proc.kill()
      rmSync(dir, { recursive: true, force: true })
    }
    rmSync(files, { recursive: true, force: true })
  }
}
