import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";
import { LanguagePolicyField } from "./language-policy-field";

const TEST_RESOURCES = { en: { common: enCommon, settings: enSettings } };

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

const FIELD_LABEL = "Agent language";

async function pickOption(optionLabel: string) {
  const user = userEvent.setup();
  await user.click(screen.getByRole("combobox", { name: FIELD_LABEL }));
  await user.click(await screen.findByRole("option", { name: optionLabel }));
  return user;
}

describe("LanguagePolicyField", () => {
  it("renders the unset state as the explicit default option (fallback)", () => {
    render(
      <I18nWrapper>
        <LanguagePolicyField value={null} onChange={vi.fn()} />
      </I18nWrapper>,
    );
    const trigger = screen.getByRole("combobox", { name: FIELD_LABEL });
    expect(trigger).toHaveTextContent("Default (no policy)");
  });

  it("reads back a stored policy value as its option label", () => {
    render(
      <I18nWrapper>
        <LanguagePolicyField value="ru" onChange={vi.fn()} />
      </I18nWrapper>,
    );
    const trigger = screen.getByRole("combobox", { name: FIELD_LABEL });
    expect(trigger).toHaveTextContent("Russian");
  });

  it("selecting a language reports the BCP-47 code", async () => {
    const onChange = vi.fn();
    render(
      <I18nWrapper>
        <LanguagePolicyField value={null} onChange={onChange} />
      </I18nWrapper>,
    );
    await pickOption("Russian");
    expect(onChange).toHaveBeenCalledWith("ru");
  });

  it("selecting the default option clears the policy (null)", async () => {
    const onChange = vi.fn();
    render(
      <I18nWrapper>
        <LanguagePolicyField value="ru" onChange={onChange} />
      </I18nWrapper>,
    );
    await pickOption("Default (no policy)");
    expect(onChange).toHaveBeenCalledWith(null);
  });

  it("disables the control when disabled is set", () => {
    render(
      <I18nWrapper>
        <LanguagePolicyField value="ru" disabled onChange={vi.fn()} />
      </I18nWrapper>,
    );
    expect(
      screen.getByRole("combobox", { name: FIELD_LABEL }),
    ).toBeDisabled();
  });
});
