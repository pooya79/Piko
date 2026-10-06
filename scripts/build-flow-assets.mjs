import { copyFile, mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

// Ship pinned browser distributions locally, with their license notices.
await mkdir("static/vendor", { recursive: true });
await copyFile("assets/flow-renderer.css", "static/vendor/flow-renderer.css");
for (const name of ["cytoscape", "cytoscape-dagre"]) {
  const root = dirname(dirname(fileURLToPath(import.meta.resolve(name))));
  await copyFile(join(root, "dist", `${name}.min.js`), `static/vendor/${name}.min.js`);
  const license = await readFile(join(root, "LICENSE"), "utf8");
  await writeFile(`static/vendor/${name}.LICENSE.txt`, license);
}
