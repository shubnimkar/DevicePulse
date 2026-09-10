import type { MetadataRoute } from 'next';

const siteUrl = process.env.NEXT_PUBLIC_SITE_URL ?? 'https://devicepulse.waybeyond.tech';

export default function sitemap(): MetadataRoute.Sitemap {
  const lastModified = new Date('2026-09-10');

  return [
    {
      url: `${siteUrl}/platform`,
      lastModified,
      changeFrequency: 'monthly',
      priority: 1,
    },
    ...['privacy', 'terms', 'security', 'data-processing'].map(path => ({
      url: `${siteUrl}/${path}`,
      lastModified,
      changeFrequency: 'yearly' as const,
      priority: 0.3,
    })),
  ];
}
