# Build the Bot studio on the existing Go-rendered frontend

Implement the Bot studio with Go/templ and focused JavaScript for streaming, pane switching, Flow canvas inspection, and updating committed state. The archived React design supplies the visual and interaction direction; React remains outside the application code. We choose this over a React studio to retain the existing server-rendered authorization and presentation boundaries, accepting that the reference layouts and components must be adapted rather than copied as JSX.
