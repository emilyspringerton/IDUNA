// webglSupport.ts -- real fix for a real, live-reported crash. `THREE.WebGLRenderer` throws
// synchronously when the browser can't create a WebGL context (disabled hardware acceleration, a
// sandboxed/locked-down browser policy, software rendering blocked, etc.) -- founder real-time,
// 2026-09-28, confirmed via their own real console error: "SHANKPIT levels is a blank screen" ->
// "Uncaught Error: THREE.WebGLRenderer: Error creating WebGL context" / "BindToCurrentSequence
// failed". Six 3D-viewport components across NOCK (ShankpitLevelEditor, ShankpitWidgets, Robots,
// Animator, AnimationEditor, AnimationViewer) construct a WebGLRenderer inside a one-time setup
// useEffect; with no ErrorBoundary anywhere in this app (see App.tsx's own new TabErrorBoundary,
// added the same pass as this file), that throw propagates up through React's own commit phase
// and unmounts the ENTIRE app, not just the one tab using it. This wraps construction in a
// try/catch so a WebGL-unavailable browser gets one readable message in that one tab instead.
import * as THREE from 'three'

export function createWebglRenderer(options: THREE.WebGLRendererParameters): THREE.WebGLRenderer | null {
  try {
    return new THREE.WebGLRenderer(options)
  } catch (err) {
    console.error('WebGL unavailable:', err instanceof Error ? err.message : err)
    return null
  }
}

export const WEBGL_UNAVAILABLE_MESSAGE =
  "This 3D view needs WebGL, which your browser couldn't provide (hardware acceleration may be " +
  'disabled, or WebGL blocked by a sandboxed browser policy). Try enabling hardware acceleration ' +
  '(chrome://settings → System) or a different browser.'
