import en from "./locales/en.json";
import itLocale from "./locales/it.json";

function keys(obj: object, prefix = ""): string[] {
  return Object.entries(obj).flatMap(([k, v]) =>
    v !== null && typeof v === "object" ? keys(v, `${prefix}${k}.`) : [`${prefix}${k}`],
  );
}

describe("translations", () => {
  it("Italian has exactly the same keys as English", () => {
    expect(keys(itLocale).sort()).toEqual(keys(en).sort());
  });

  it("has no empty strings", () => {
    for (const [name, dict] of Object.entries({ en, it: itLocale })) {
      const empty = keys(dict).filter((k) => {
        const value = k
          .split(".")
          .reduce<unknown>((o, p) => (o as Record<string, unknown>)[p], dict);
        return typeof value === "string" && value.trim() === "";
      });
      expect(empty, name).toEqual([]);
    }
  });
});
