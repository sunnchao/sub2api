import { afterEach, describe, expect, it } from "vitest";
import {
  BUILTIN_PLATFORM_CATALOG,
  resetPlatformCatalog,
  setPlatformCatalog,
} from "@/constants/platformCatalog";

import {
  defaultAllowImageGenerationForPlatform,
  getDefaultImagePreviewPrice,
  getDefaultVideoPreviewPrice,
  getImagePricePlaceholder,
  getVideoPricePlaceholder,
  imagePricingPlatforms,
  imagePricingI18nKey,
  supportsImagePricingPlatform,
  supportsVideoPricingPlatform,
  videoPricingI18nKey,
} from "../groupsImagePricing";

describe("groups image pricing platform support", () => {
  it("includes Grok image groups", () => {
    expect(supportsImagePricingPlatform("grok")).toBe(true);
    expect(imagePricingPlatforms.has("grok")).toBe(true);
  });

  it("enables video pricing controls for Grok only", () => {
    expect(supportsVideoPricingPlatform("grok")).toBe(true);
    expect(supportsVideoPricingPlatform("openai")).toBe(false);
  });

  it("keeps non-media group platforms out of the image pricing controls", () => {
    expect(supportsImagePricingPlatform("anthropic")).toBe(false);
  });

  it("defaults image generation on for Grok and Gemini-family groups only", () => {
    expect(defaultAllowImageGenerationForPlatform("grok")).toBe(true);
    expect(defaultAllowImageGenerationForPlatform("gemini")).toBe(true);
    expect(defaultAllowImageGenerationForPlatform("antigravity")).toBe(true);
    expect(defaultAllowImageGenerationForPlatform("composite")).toBe(false);
    expect(defaultAllowImageGenerationForPlatform("openai")).toBe(false);
  });

  it("includes Composite groups in image pricing controls", () => {
    expect(supportsImagePricingPlatform("composite")).toBe(true);
    expect(imagePricingPlatforms.has("composite")).toBe(true);
  });

  it("keeps image and video pricing copy separate", () => {
    expect(imagePricingI18nKey("grok", "title")).toBe(
      "admin.groups.imagePricing.title",
    );
    expect(videoPricingI18nKey("title")).toBe("admin.groups.videoPricing.title");
  });

  it("uses Grok media defaults instead of generic image fallback placeholders", () => {
    expect(getImagePricePlaceholder("grok", "image_price_1k")).toBe("0.02");
    expect(getImagePricePlaceholder("grok", "image_price_2k")).toBe("0.02");
    // 视频 placeholder 为每秒单价：480p/720p 取 grok-imagine-video 官方每秒价，
    // 1080p 仅 video-1.5 支持、取 1.5 每秒价。
    expect(getVideoPricePlaceholder("grok", "video_price_480p")).toBe("0.05");
    expect(getVideoPricePlaceholder("grok", "video_price_720p")).toBe("0.07");
    expect(getVideoPricePlaceholder("grok", "video_price_1080p")).toBe("0.25");
  });

  it("keeps non-Grok image placeholders on the generic image card", () => {
    expect(getImagePricePlaceholder("openai", "image_price_1k")).toBe("0.134");
    expect(getDefaultImagePreviewPrice("openai", "image_price_2k")).toBe(0.201);
    expect(getDefaultVideoPreviewPrice("openai", "video_price_480p")).toBeNull();
  });

  describe("custom platforms with image capability", () => {
    afterEach(() => {
      resetPlatformCatalog();
    });

    it("enables image pricing controls and default allow_image_generation only when supports_images is true", () => {
      setPlatformCatalog({
        platforms: [
          ...BUILTIN_PLATFORM_CATALOG.platforms,
          {
            id: "flux_cloud",
            display_name: "Flux Cloud",
            gateway: "openai",
            cn_provider: false,
            custom: true,
            supports_images: true,
          },
          {
            id: "chat_vendor",
            display_name: "Chat Vendor",
            gateway: "openai",
            cn_provider: false,
            custom: true,
            supports_images: false,
          },
        ],
        composite_precedence: BUILTIN_PLATFORM_CATALOG.composite_precedence,
      });

      expect(supportsImagePricingPlatform("flux_cloud")).toBe(true);
      expect(defaultAllowImageGenerationForPlatform("flux_cloud")).toBe(true);
      expect(supportsImagePricingPlatform("chat_vendor")).toBe(false);
      expect(defaultAllowImageGenerationForPlatform("chat_vendor")).toBe(false);
    });
  });
});
