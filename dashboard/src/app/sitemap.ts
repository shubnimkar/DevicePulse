import type { MetadataRoute } from 'next';

const siteUrl = process.env.NEXT_PUBLIC_SITE_URL ?? 'https://devicepulse.waybeyond.tech';

export default function sitemap(): MetadataRoute.Sitemap {
  return [
    {
      url: `${siteUrl}/platform`,
      lastModified: new Date('2026-09-10'),
      changeFrequency: 'monthly',
      priority: 1,
    },
  ];
}
