declare module "@gox/actions" {
  export class GoxActionError extends Error {
    readonly code: number
    readonly type: string
    readonly action: string
    readonly status: number
    readonly cause?: unknown
  }

  export interface ServerActions {}

  const Actions: ServerActions
  export default Actions
}
