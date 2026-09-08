import { readdir, readFile } from "node:fs/promises"
import { extname, join, relative } from "node:path"

const root = new URL("../src", import.meta.url).pathname.replace(/^\/(.:)/, "$1")
const allowed = join(root, "components", "ui")
const forbidden = /<(button|input|textarea|select|dialog|table)(\s|>)/g
const forbiddenInlineIcon = /<svg(\s|>)|[✓⌄×☾☀◐]|icon:\s*["'][A-Z]["']/g
const failures = []

async function walk(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const file = join(directory, entry.name)
    if (entry.isDirectory()) {
      await walk(file)
      continue
    }
    if (![".tsx", ".jsx"].includes(extname(file))) continue
    const source = await readFile(file, "utf8")
    if (!file.startsWith(allowed)) {
      for (const match of source.matchAll(forbidden)) {
        const line = source.slice(0, match.index).split("\n").length
        failures.push(`${relative(root, file)}:${line} raw <${match[1]}> is only allowed in components/ui`)
      }
    }
    for (const match of source.matchAll(forbiddenInlineIcon)) {
      const line = source.slice(0, match.index).split("\n").length
      failures.push(`${relative(root, file)}:${line} use a lucide-react component instead of an inline or text icon`)
    }
  }
}

await walk(root)
if (failures.length) {
  console.error(failures.join("\n"))
  process.exit(1)
}
console.log("UI boundary check passed")
