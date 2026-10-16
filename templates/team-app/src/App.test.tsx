import { afterEach, expect, test } from "bun:test";
import { cleanup, render } from "@testing-library/react";
import { App } from "./App";

afterEach(cleanup);

test("renders the page", () => {
  const { container } = render(<App />);
  expect(container.querySelector("main")).toBeTruthy();
});
