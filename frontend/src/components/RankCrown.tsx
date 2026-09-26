// Rank medic crowns — design.md §7.3, viewBox 0 0 36 34.
//
// Three genuinely different silhouettes (not one medal recolored). Each is a
// `.crown-face` + `.crown-depth` (extruded side) behind a `.crown-base` line.
// Colours come from `data-rank` via the `.landing-root` tokens (--gold/--silver/
// --bronze) with the design.md §7.3 light-mode fallbacks in index.css.
//
// The outline points and the shallow-3D projection are ported verbatim from the
// live `monitor-v12.js` crown builder (Euler(-0.10, -0.20, 0), z=±2, centred at
// (16,16), projected to (18,17)); no WebGL/Three.js dependency.

type Pt = [number, number];

const OUTLINES: (Pt[] | null)[] = [
  null,
  [[4, 25], [2, 10], [10, 15], [16, 3], [22, 15], [30, 10], [28, 25]],
  [[5, 25], [3, 8], [11, 16], [16, 8], [21, 16], [29, 8], [27, 25]],
  [[6, 25], [3, 13], [11, 17], [16, 10], [21, 17], [29, 13], [26, 25]],
];

function pathFor(pts: readonly Pt[]): string {
  return pts.map((p, i) => `${i ? "L" : "M"}${p[0]} ${p[1]}`).join("") + "Z";
}

function project(x: number, y: number, z: number, ex: number, ey: number): [number, number] {
  // THREE.Euler(ex, ey, 0) -> quaternion, then rotate v = (x-16, 16-y, z).
  const c1 = Math.cos(ex / 2), c2 = Math.cos(ey / 2), c3 = 1;
  const s1 = Math.sin(ex / 2), s2 = Math.sin(ey / 2), s3 = 0;
  const qx = s1 * c2 * c3 + c1 * s2 * s3;
  const qy = c1 * s2 * c3 - s1 * c2 * s3;
  const qz = c1 * c2 * s3 + s1 * s2 * c3;
  const qw = c1 * c2 * c3 - s1 * s2 * s3;
  const px = x - 16;
  const py = 16 - y;
  const ix = qw * px + qy * z - qz * py;
  const iy = qw * py + qz * px - qx * z;
  const iz = qw * z + qx * py - qy * px;
  const iw = -qx * px - qy * py - qz * z;
  const rx = ix * qw + iw * -qx + iy * -qz - iz * -qy;
  const ry = iy * qw + iw * -qy + iz * -qx - ix * -qz;
  return [18 + rx, 17 - ry];
}

function geometry(rank: 1 | 2 | 3, ex: number, ey: number) {
  const outline = OUTLINES[rank]!;
  const front = outline.map((p) => project(p[0], p[1], 2, ex, ey));
  const rear = outline.map((p) => project(p[0], p[1], -2, ex, ey));
  const n = front.length;
  const sides = front.map((p, i) => pathFor([p, front[(i + 1) % n], rear[(i + 1) % n], rear[i]]));
  return { face: pathFor(front), depth: sides.join(" ") };
}

export function RankCrown({ rank, size = 32 }: { rank: 1 | 2 | 3; size?: number }) {
  const { face, depth } = geometry(rank, -0.1, -0.2);
  return (
    <span
      className="rank-crown block leading-none"
      data-rank={rank}
      style={{ width: size, height: size }}
      aria-hidden="true"
    >
      <svg viewBox="0 0 36 34" width={size} height={size} className="overflow-visible">
        <path className="crown-depth" d={depth} />
        <path className="crown-face" d={face} />
        <path className="crown-base" d="M7 28H25" />
      </svg>
    </span>
  );
}
