import { readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";

// Keep the browser asset small: only the official Phosphor icons used by templ pages.
// The MIT notice for these SVGs ships beside the generated sprite in static/.
const icons = {
 "arrow-right": ["regular", "arrow-right"],
};

async function source(weight, name) {
  const suffix = weight === "regular" ? "" : `-${weight}`;
  const url = import.meta.resolve(`@phosphor-icons/core/assets/${weight}/${name}${suffix}.svg`);
  const svg = await readFile(fileURLToPath(url), "utf8");
  const match = svg.match(/<svg[^>]*viewBox="([^"]+)"[^>]*>([\s\S]*?)<\/svg>/);
  if (!match) throw new Error(`Unexpected Phosphor SVG: ${weight}/${name}`);
  return { viewBox: match[1], paths: match[2] };
}

const symbols = await Promise.all(Object.entries(icons).map(async ([id, [weight, name]]) => {
  const { viewBox, paths } = await source(weight, name);
  return `  <symbol id="${id}" viewBox="${viewBox}">${paths}</symbol>`;
}));
await writeFile("static/icons.svg",
  `<svg xmlns="http://www.w3.org/2000/svg">\n${symbols.join("\n")}\n</svg>\n`);
