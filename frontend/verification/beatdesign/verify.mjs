// Run the pinned upstream Node tests against the exact modules shipped by the Vue workspace.
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { spawnSync } from 'node:child_process'
import ts from 'typescript'

const upstream = new URL('../../src/features/creative-canvas/upstream/', import.meta.url)
const tests = ['canvas-placement.test', 'upload-layout.test', 'generation-history.test', 'video-frame.test']
const temp = await mkdtemp(join(tmpdir(), 'sub2api-canvas-tests-'))
try {
  for (const [root, names] of [[upstream, ['canvas-types', 'upload-layout', 'generation-history', 'video-frame']], [new URL('./', import.meta.url), tests]]) {
    for (const name of names) {
      const input = await readFile(new URL(`${name}.ts`, root), 'utf8')
      const { outputText } = ts.transpileModule(input, { compilerOptions: {
        target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS, esModuleInterop: true,
      } })
      await writeFile(join(temp, `${name}.js`), outputText)
    }
  }
  const run = spawnSync(process.execPath, ['--test', ...tests.map(name => `${name}.js`)], { cwd: temp, stdio: 'inherit' })
  if (run.error) throw run.error
  process.exitCode = run.status ?? 1
} finally {
  await rm(temp, { recursive: true, force: true })
}
