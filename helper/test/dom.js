import { GlobalRegistrator } from "@happy-dom/global-registrator";

// A browser's document and window from happy-dom, on a page of a hi
// app. Bun's Response stays: the fake app service streams its answers
// with it.
const { Response, ReadableStream, TextDecoder, TextEncoder } = globalThis;
GlobalRegistrator.register({ url: "http://localhost:3000/orders", width: 1280, height: 800 });
Object.assign(globalThis, { Response, ReadableStream, TextDecoder, TextEncoder });
