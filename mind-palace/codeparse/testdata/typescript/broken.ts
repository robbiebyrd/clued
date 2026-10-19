// A file with a syntax error: what the query still matches is kept.

export interface Point {
  x: number;
  y: number;
}

export function distance(a: Point, b: Point): number {
  return Math.hypot(a.x - b.x, a.y - b.y;
}

export class Shape {
  area(): number {
    return compute(this);
  }
}

export const ORIGIN: Point = { x: 0, y: 0 };
