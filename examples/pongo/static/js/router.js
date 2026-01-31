/**
 * HotStatic Router - SPA-like navigation for static sites
 * Features: fade transitions, prefetch on hover, progress bar
 */
(function() {
    'use strict';

    // Configuration
    const CONFIG = {
        contentSelector: 'main',          // Element to replace
        transitionDuration: 200,          // Fade duration in ms
        prefetchDelay: 100,               // Delay before prefetch on hover
        progressBarColor: '#3b82f6',      // Progress bar color
        progressBarHeight: '3px',         // Progress bar height
    };

    // Cache for prefetched pages
    const cache = new Map();

    // Prefetch timeout
    let prefetchTimeout = null;

    // Current navigation abort controller
    let abortController = null;

    // ==================== Progress Bar ====================

    const progressBar = document.createElement('div');
    progressBar.id = 'hs-progress';
    progressBar.innerHTML = `
        <style>
            #hs-progress {
                position: fixed;
                top: 0;
                left: 0;
                width: 0;
                height: ${CONFIG.progressBarHeight};
                background: ${CONFIG.progressBarColor};
                z-index: 99999;
                transition: width 0.2s ease, opacity 0.2s ease;
                box-shadow: 0 0 10px ${CONFIG.progressBarColor};
            }
            #hs-progress.loading {
                width: 70%;
                transition: width 10s cubic-bezier(0.1, 0.5, 0.1, 1);
            }
            #hs-progress.done {
                width: 100%;
                transition: width 0.1s ease;
            }
            #hs-progress.hide {
                opacity: 0;
            }
        </style>
    `;
    document.body.appendChild(progressBar);

    function showProgress() {
        progressBar.classList.remove('done', 'hide');
        progressBar.style.width = '0';
        // Force reflow
        progressBar.offsetHeight;
        progressBar.classList.add('loading');
    }

    function completeProgress() {
        progressBar.classList.remove('loading');
        progressBar.classList.add('done');
        setTimeout(() => {
            progressBar.classList.add('hide');
            setTimeout(() => {
                progressBar.style.width = '0';
                progressBar.classList.remove('done', 'hide');
            }, 200);
        }, 150);
    }

    // ==================== Page Fetching ====================

    async function fetchPage(url, signal) {
        // Check cache first
        if (cache.has(url)) {
            return cache.get(url);
        }

        const response = await fetch(url, { signal });
        if (!response.ok) {
            throw new Error(`HTTP ${response.status}`);
        }

        const html = await response.text();
        const doc = new DOMParser().parseFromString(html, 'text/html');

        const result = {
            title: doc.title,
            content: doc.querySelector(CONFIG.contentSelector)?.innerHTML || '',
            scripts: Array.from(doc.querySelectorAll(`${CONFIG.contentSelector} script`)),
        };

        // Cache the result
        cache.set(url, result);

        return result;
    }

    // ==================== Navigation ====================

    async function navigate(url, pushState = true) {
        // Abort any ongoing navigation
        if (abortController) {
            abortController.abort();
        }
        abortController = new AbortController();

        const content = document.querySelector(CONFIG.contentSelector);
        if (!content) {
            window.location.href = url;
            return;
        }

        showProgress();

        try {
            // Fade out
            content.style.transition = `opacity ${CONFIG.transitionDuration}ms ease`;
            content.style.opacity = '0';

            // Fetch page (with small delay to let fade start)
            const [page] = await Promise.all([
                fetchPage(url, abortController.signal),
                new Promise(r => setTimeout(r, CONFIG.transitionDuration))
            ]);

            // Update content
            content.innerHTML = page.content;

            // Update title
            document.title = page.title;

            // Update URL
            if (pushState) {
                history.pushState({ url }, page.title, url);
            }

            // Scroll to top
            window.scrollTo({ top: 0, behavior: 'instant' });

            // Fade in
            content.style.opacity = '1';

            // Execute inline scripts
            page.scripts.forEach(oldScript => {
                const newScript = document.createElement('script');
                Array.from(oldScript.attributes).forEach(attr => {
                    newScript.setAttribute(attr.name, attr.value);
                });
                newScript.textContent = oldScript.textContent;
                content.appendChild(newScript);
            });

            // Dispatch custom event
            window.dispatchEvent(new CustomEvent('hs:navigate', {
                detail: { url, title: page.title }
            }));

            completeProgress();

        } catch (error) {
            if (error.name === 'AbortError') {
                return; // Navigation was cancelled
            }

            console.error('Navigation failed:', error);
            completeProgress();

            // Fallback to full page load
            window.location.href = url;
        }
    }

    // ==================== Prefetch ====================

    function prefetch(url) {
        if (cache.has(url)) return;

        // Use low priority fetch
        const link = document.createElement('link');
        link.rel = 'prefetch';
        link.href = url;
        document.head.appendChild(link);

        // Also fetch and cache
        fetchPage(url, new AbortController().signal).catch(() => {});
    }

    // ==================== Event Handlers ====================

    function isInternalLink(link) {
        if (!link || !link.href) return false;
        if (link.target === '_blank') return false;
        if (link.hasAttribute('data-no-router')) return false;
        if (link.hasAttribute('download')) return false;

        const url = new URL(link.href, window.location.origin);

        // Must be same origin
        if (url.origin !== window.location.origin) return false;

        // Must be HTML page (no extension or .html)
        const path = url.pathname;
        const ext = path.split('.').pop();
        if (ext && !['html', 'htm'].includes(ext) && path.includes('.')) return false;

        return true;
    }

    // Click handler
    document.addEventListener('click', (e) => {
        const link = e.target.closest('a');
        if (!isInternalLink(link)) return;
        if (e.ctrlKey || e.metaKey || e.shiftKey) return;

        e.preventDefault();

        const url = link.href;
        if (url === window.location.href) return;

        navigate(url);
    });

    // Hover handler for prefetch
    document.addEventListener('mouseover', (e) => {
        const link = e.target.closest('a');
        if (!isInternalLink(link)) return;

        clearTimeout(prefetchTimeout);
        prefetchTimeout = setTimeout(() => {
            prefetch(link.href);
        }, CONFIG.prefetchDelay);
    });

    document.addEventListener('mouseout', (e) => {
        if (e.target.closest('a')) {
            clearTimeout(prefetchTimeout);
        }
    });

    // Touch handler for mobile prefetch
    document.addEventListener('touchstart', (e) => {
        const link = e.target.closest('a');
        if (isInternalLink(link)) {
            prefetch(link.href);
        }
    }, { passive: true });

    // Back/forward navigation
    window.addEventListener('popstate', (e) => {
        if (e.state?.url) {
            navigate(e.state.url, false);
        } else {
            navigate(window.location.href, false);
        }
    });

    // Set initial state
    history.replaceState({ url: window.location.href }, document.title);

    // ==================== Public API ====================

    window.HotStaticRouter = {
        // Manual navigation
        navigate: (url) => navigate(url),

        // Prefetch a URL
        prefetch: (url) => prefetch(url),

        // Clear cache
        clearCache: () => cache.clear(),

        // Get cached URLs
        getCached: () => Array.from(cache.keys()),

        // Configuration
        config: CONFIG,
    };

    console.log('🚀 HotStatic Router initialized');
})();
