import fs from "fs"
import path from "path"
import os from "os"
import { fileURLToPath } from "url"
import type { PluginInput } from "@opencode-ai/plugin"

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const ASSETS_DIR = path.resolve(__dirname, "..", "assets")
const VERSION_FILE = ".thrum-plugin-version"

function getPluginVersion(): string {
  const pkgPath = path.resolve(__dirname, "..", "package.json")
  try {
    const pkg = JSON.parse(fs.readFileSync(pkgPath, "utf8"))
    return pkg.version ?? "0.0.0"
  } catch {
    return "0.0.0"
  }
}

function getConfigDir(): string {
  const xdg = process.env.XDG_CONFIG_HOME
  if (xdg) return path.join(xdg, "opencode")
  return path.join(os.homedir(), ".config", "opencode")
}

function copyDirRecursive(src: string, dest: string) {
  fs.mkdirSync(dest, { recursive: true })
  for (const entry of fs.readdirSync(src, { withFileTypes: true })) {
    const srcPath = path.join(src, entry.name)
    const destPath = path.join(dest, entry.name)
    if (entry.isDirectory()) {
      copyDirRecursive(srcPath, destPath)
    } else {
      fs.copyFileSync(srcPath, destPath)
    }
  }
}

export async function installAssets(ctx: PluginInput) {
  const configDir = getConfigDir()
  const skillsDir = path.join(configDir, "skills")
  const commandsDir = path.join(configDir, "commands")
  const versionPath = path.join(skillsDir, "thrum", VERSION_FILE)

  const currentVersion = getPluginVersion()

  // Check if already installed at this version
  try {
    const installed = fs.readFileSync(versionPath, "utf8").trim()
    if (installed === currentVersion) return
  } catch {
    // Not installed yet
  }

  const log = (msg: string) =>
    ctx.client.app.log({
      body: { service: "opencode-thrum", level: "info", message: msg },
    })

  // Copy each skill subdirectory
  const assetsSkillsDir = path.join(ASSETS_DIR, "skills")
  if (fs.existsSync(assetsSkillsDir)) {
    for (const entry of fs.readdirSync(assetsSkillsDir, {
      withFileTypes: true,
    })) {
      if (!entry.isDirectory()) continue
      const src = path.join(assetsSkillsDir, entry.name)
      const dest = path.join(skillsDir, entry.name)
      copyDirRecursive(src, dest)
      await log(`Installed skill: ${entry.name}`)
    }
  }

  // Copy commands
  const assetsCommandsDir = path.join(ASSETS_DIR, "commands")
  if (fs.existsSync(assetsCommandsDir)) {
    fs.mkdirSync(commandsDir, { recursive: true })
    let count = 0
    for (const entry of fs.readdirSync(assetsCommandsDir)) {
      if (!entry.endsWith(".md")) continue
      fs.copyFileSync(
        path.join(assetsCommandsDir, entry),
        path.join(commandsDir, entry),
      )
      count++
    }
    await log(`Installed ${count} commands`)
  }

  // Write version marker
  fs.mkdirSync(path.dirname(versionPath), { recursive: true })
  fs.writeFileSync(versionPath, currentVersion)
  await log(`opencode-thrum v${currentVersion} assets installed`)
}
