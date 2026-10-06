# Build the Bot studio on the existing Go-rendered frontend

Implement the Bot studio with Go/templ and focused JavaScript for streaming, pane switching, and updating committed state. The separate Flow tab follows the same frontend boundary for Flow canvas inspection. The archived React design supplies the visual and interaction direction; React remains outside the application code. We choose this over a React studio to retain the existing server-rendered authorization and presentation boundaries, accepting that the reference layouts and components must be adapted rather than copied as JSX.

Flow uses locally served, pinned Cytoscape.js and Dagre for connections and automatic layout, with HTML nodes and a templ inspector for Persian text, icons and keyboard access. Cytoscape's default container stylesheet is served as a local asset under its recognized stylesheet ID, so the renderer does not inject inline CSS or require a weaker Content Security Policy.
