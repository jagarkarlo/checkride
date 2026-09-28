import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

export default defineConfig({
  integrations: [starlight({
    title: 'Checkride',
    description: 'Recovery drills for Kubernetes applications. Restore, inspect, and prove what survived.',
    social: [{ icon: 'github', label: 'Source code', href: 'https://github.com/jagarkarlo/checkride' }],
    sidebar: [
      { label: 'Product', items: [
        { label: 'Overview', link: '/' },
        { label: 'System design', link: '/product/' },
        { label: 'Evidence model', link: '/evidence/' },
        { label: 'Roadmap', link: '/roadmap/' },
      ] },
      { label: 'Start here', items: [
        { label: 'Overview', link: '/docs/' },
        { label: 'Run locally', slug: 'start' },
      ] },
      { label: 'Understand', items: [
        { label: 'Verification levels', slug: 'levels' },
        { label: 'Comparison', slug: 'comparison' },
      ] },
    ],
    customCss: ['./src/styles/theme.css'],
  })],
});