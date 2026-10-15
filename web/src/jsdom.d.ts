// 最小 jsdom 类型声明: @types/jsdom 未安装, 而依赖白名单只允许新增 @codemirror/theme-one-dark,
// 因此只声明测试里实际用到的部分 (JSDOM 构造 + window)。
declare module 'jsdom' {
  export class JSDOM {
    constructor(html?: string, options?: { url?: string })
    readonly window: Window & typeof globalThis
  }
}
