import { Bounds, Grid, Html, OrbitControls, useGLTF } from '@react-three/drei'
import { Canvas, type ThreeEvent } from '@react-three/fiber'
import { Suspense, useMemo, useState } from 'react'
import * as THREE from 'three'

const ERROR_COLOR = new THREE.Color('#ff3b3b')

/** Walk up from a mesh to the node named after its part (children of the assembly root). */
function partName(obj: THREE.Object3D): string | null {
  let o: THREE.Object3D | null = obj
  while (o && o.parent && o.parent.name !== 'assembly') o = o.parent
  return o?.name || null
}

function Model({ url, errorParts, xray, onHover }: {
  url: string
  errorParts: Set<string>
  xray: boolean
  onHover: (name: string | null) => void
}) {
  const { scene } = useGLTF(url)
  const model = useMemo(() => {
    const root = scene.clone(true)
    root.traverse((o) => {
      if (!(o instanceof THREE.Mesh)) return
      const name = partName(o)
      const mat = (o.material as THREE.MeshStandardMaterial).clone()
      mat.metalness = 0.3
      mat.roughness = 0.55
      if (name && errorParts.has(name)) {
        mat.color = ERROR_COLOR.clone()
        mat.emissive = ERROR_COLOR.clone().multiplyScalar(0.35)
      }
      if (name === 'bus' && xray) {
        mat.transparent = true
        mat.opacity = 0.18
        mat.depthWrite = false
      }
      o.material = mat
    })
    return root
  }, [scene, errorParts, xray])

  return (
    <primitive
      object={model}
      onPointerMove={(e: ThreeEvent<PointerEvent>) => {
        e.stopPropagation()
        onHover(partName(e.object))
      }}
      onPointerOut={() => onHover(null)}
    />
  )
}

export function Viewer({ url, errorParts }: { url: string | null; errorParts: string[] }) {
  const [xray, setXray] = useState(true)
  const [hovered, setHovered] = useState<string | null>(null)
  const errors = useMemo(() => new Set(errorParts), [errorParts])

  return (
    <div className="viewer">
      <Canvas camera={{ position: [0.6, 0.45, 0.6], fov: 40, near: 0.001, far: 100 }}>
        <color attach="background" args={['#05070d']} />
        <ambientLight intensity={0.5} />
        <directionalLight position={[2, 3, 1.5]} intensity={2.2} />
        <directionalLight position={[-2, -1, -1.5]} intensity={0.6} color="#8fb3ff" />
        <Grid infiniteGrid cellSize={0.05} sectionSize={0.25} fadeDistance={4} cellColor="#1b2233" sectionColor="#2a3550" />
        {url && (
          <Suspense fallback={<Html center className="viewer-hint">Loading model…</Html>}>
            <Bounds fit clip observe margin={1.3} key={url}>
              <Model url={url} errorParts={errors} xray={xray} onHover={setHovered} />
            </Bounds>
          </Suspense>
        )}
        <OrbitControls makeDefault />
      </Canvas>
      {!url && <div className="viewer-empty">Your spacecraft will appear here.</div>}
      <div className="viewer-toolbar">
        <label>
          <input type="checkbox" checked={xray} onChange={(e) => setXray(e.target.checked)} /> X-ray bus
        </label>
        {hovered && <span className="hovered">{hovered}</span>}
      </div>
    </div>
  )
}
