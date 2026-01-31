/**
 * HotStatic Router v2 - SPA-like navigation for static sites
 *
 * Features:
 * - Progress bar (configurable color, height, position)
 * - Prefetch on hover or when visible
 * - Cache with TTL and max pages limit
 * - Transitions: fade, slide, none
 * - Events: beforeNavigate, afterNavigate, prefetch
 *
 * Configuration via window.HotStaticConfig or data-hotstatic attribute
 *
 * Events:
 *   hs:beforeNavigate - fires before navigation (cancelable)
 *   hs:afterNavigate  - fires after navigation complete
 *   hs:prefetch       - fires when page is prefetched
 */
(function () {
  "use strict";

  // ==================== Default Configuration ====================

  const DEFAULT_CONFIG = {
    // Content selector to replace
    contentSelector: "main",

    // Progress bar
    progressBar: {
      enabled: true,
      color: "#3b82f6",
      height: "3px",
      position: "top", // 'top' | 'bottom'
    },

    // Prefetch
    prefetch: {
      enabled: true,
      on: "hover", // 'hover' | 'visible' | 'both'
      delay: 100, // ms delay before prefetch on hover
    },

    // Cache
    cache: {
      enabled: true,
      maxPages: 20,
      ttl: 300, // seconds
    },

    // Navigation
    navigation: {
      enabled: true,
      transition: "fade", // 'fade' | 'slide' | 'none'
      duration: 150, // ms
    },
  };

  // ==================== Merge Config ====================

  function mergeConfig(defaults, overrides) {
    const result = { ...defaults };
    for (const key in overrides) {
      if (
        overrides[key] !== null &&
        typeof overrides[key] === "object" &&
        !Array.isArray(overrides[key])
      ) {
        result[key] = mergeConfig(defaults[key] || {}, overrides[key]);
      } else {
        result[key] = overrides[key];
      }
    }
    return result;
  }

  // Get config from window or data attribute
  const userConfig = window.HotStaticConfig || {};
  const CONFIG = mergeConfig(DEFAULT_CONFIG, userConfig);

  // ==================== Cache with TTL ====================

  class PageCache {
    constructor(maxPages, ttlSeconds) {
      this.maxPages = maxPages;
      this.ttl = ttlSeconds * 1000;
      this.cache = new Map();
    }

    get(url) {
      const entry = this.cache.get(url);
      if (!entry) return null;

      // Check TTL
      if (Date.now() - entry.timestamp > this.ttl) {
        this.cache.delete(url);
        return null;
      }

      return entry.data;
    }

    set(url, data) {
      // Enforce max pages limit
      if (this.cache.size >= this.maxPages) {
        // Remove oldest entry
        const oldestKey = this.cache.keys().next().value;
        this.cache.delete(oldestKey);
      }

      this.cache.set(url, {
        data,
        timestamp: Date.now(),
      });
    }

    has(url) {
      return this.get(url) !== null;
    }

    clear() {
      this.cache.clear();
    }

    keys() {
      return Array.from(this.cache.keys());
    }
  }

  const cache = CONFIG.cache.enabled
    ? new PageCache(CONFIG.cache.maxPages, CONFIG.cache.ttl)
    : {
        get: () => null,
        set: () => {},
        has: () => false,
        clear: () => {},
        keys: () => [],
      };

  // ==================== Progress Bar ====================

  let progressBar = null;

  function initProgressBar() {
    if (!CONFIG.progressBar.enabled) return;

    progressBar = document.createElement("div");
    progressBar.id = "hs-progress";

    const positionStyle =
      CONFIG.progressBar.position === "bottom"
        ? "bottom: 0; top: auto;"
        : "top: 0; bottom: auto;";

    progressBar.innerHTML = `
            <style>
                #hs-progress {
                    position: fixed;
                    ${positionStyle}
                    left: 0;
                    width: 0;
                    height: ${CONFIG.progressBar.height};
                    background: ${CONFIG.progressBar.color};
                    z-index: 99999;
                    transition: width 0.2s ease, opacity 0.2s ease;
                    box-shadow: 0 0 10px ${CONFIG.progressBar.color};
                    pointer-events: none;
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
  }

  function showProgress() {
    if (!progressBar) return;
    progressBar.classList.remove("done", "hide");
    progressBar.style.width = "0";
    progressBar.offsetHeight; // Force reflow
    progressBar.classList.add("loading");
  }

  function completeProgress() {
    if (!progressBar) return;
    progressBar.classList.remove("loading");
    progressBar.classList.add("done");
    setTimeout(() => {
      progressBar.classList.add("hide");
      setTimeout(() => {
        progressBar.style.width = "0";
        progressBar.classList.remove("done", "hide");
      }, 200);
    }, 150);
  }

  // ==================== Transitions ====================

  function applyTransitionOut(element) {
    const duration = CONFIG.navigation.duration;
    const transition = CONFIG.navigation.transition;

    return new Promise((resolve) => {
      if (transition === "none" || duration === 0) {
        resolve();
        return;
      }

      element.style.transition = `opacity ${duration}ms ease, transform ${duration}ms ease`;

      if (transition === "fade") {
        element.style.opacity = "0";
      } else if (transition === "slide") {
        element.style.opacity = "0";
        element.style.transform = "translateX(-20px)";
      }

      setTimeout(resolve, duration);
    });
  }

  function applyTransitionIn(element) {
    const duration = CONFIG.navigation.duration;
    const transition = CONFIG.navigation.transition;

    if (transition === "none" || duration === 0) {
      element.style.opacity = "1";
      element.style.transform = "";
      return;
    }

    // Set initial state for in-transition
    if (transition === "slide") {
      element.style.transform = "translateX(20px)";
    }

    element.style.transition = `opacity ${duration}ms ease, transform ${duration}ms ease`;

    // Trigger transition
    requestAnimationFrame(() => {
      element.style.opacity = "1";
      element.style.transform = "";
    });
  }

  // ==================== Events ====================

  function dispatchEvent(name, detail, cancelable = false) {
    const event = new CustomEvent(`hs:${name}`, {
      detail,
      cancelable,
      bubbles: true,
    });
    return document.dispatchEvent(event);
  }

  // ==================== Page Fetching ====================

  async function fetchPage(url, signal) {
    // Check cache first
    const cached = cache.get(url);
    if (cached) {
      return cached;
    }

    const response = await fetch(url, { signal });
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`);
    }

    const html = await response.text();
    const doc = new DOMParser().parseFromString(html, "text/html");

    const result = {
      title: doc.title,
      content: doc.querySelector(CONFIG.contentSelector)?.innerHTML || "",
      scripts: Array.from(
        doc.querySelectorAll(`${CONFIG.contentSelector} script`),
      ),
    };

    // Cache the result
    cache.set(url, result);

    return result;
  }

  // ==================== Navigation ====================

  let abortController = null;

  async function navigate(url, pushState = true) {
    if (!CONFIG.navigation.enabled) {
      window.location.href = url;
      return;
    }

    // Dispatch beforeNavigate event (cancelable)
    const allowed = dispatchEvent(
      "beforeNavigate",
      {
        from: window.location.href,
        to: url,
      },
      true,
    );

    if (!allowed) {
      return; // Navigation cancelled by user
    }

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
      // Start transition out and fetch in parallel
      const [page] = await Promise.all([
        fetchPage(url, abortController.signal),
        applyTransitionOut(content),
      ]);

      // Update content
      content.innerHTML = page.content;

      // Update title
      document.title = page.title;

      // Update URL
      if (pushState) {
        history.pushState({ url }, page.title, url);
      }

      // Transition in
      applyTransitionIn(content);

      // Execute inline scripts
      page.scripts.forEach((oldScript) => {
        const newScript = document.createElement("script");
        Array.from(oldScript.attributes).forEach((attr) => {
          newScript.setAttribute(attr.name, attr.value);
        });
        newScript.textContent = oldScript.textContent;
        content.appendChild(newScript);
      });

      completeProgress();

      // Dispatch afterNavigate event
      dispatchEvent("afterNavigate", {
        from: window.location.href,
        to: url,
        title: page.title,
      });
    } catch (error) {
      if (error.name === "AbortError") {
        return; // Navigation was cancelled
      }

      console.error("[HotStatic] Navigation failed:", error);
      completeProgress();

      // Fallback to full page load
      window.location.href = url;
    }
  }

  // ==================== Prefetch ====================

  let prefetchTimeout = null;

  function prefetch(url) {
    if (!CONFIG.prefetch.enabled) return;
    if (cache.has(url)) return;

    // Fetch and cache
    fetchPage(url, new AbortController().signal)
      .then(() => {
        dispatchEvent("prefetch", { url });
      })
      .catch(() => {});
  }

  // ==================== Intersection Observer for Visible Prefetch ====================

  let visibleObserver = null;

  function initVisiblePrefetch() {
    if (!CONFIG.prefetch.enabled) return;
    if (CONFIG.prefetch.on !== "visible" && CONFIG.prefetch.on !== "both")
      return;

    visibleObserver = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) {
            const link = entry.target;
            if (isInternalLink(link)) {
              prefetch(link.href);
            }
            visibleObserver.unobserve(link);
          }
        });
      },
      {
        rootMargin: "50px",
      },
    );

    // Observe all links
    observeLinks();
  }

  function observeLinks() {
    if (!visibleObserver) return;
    document.querySelectorAll("a").forEach((link) => {
      if (isInternalLink(link)) {
        visibleObserver.observe(link);
      }
    });
  }

  // ==================== Link Helpers ====================

  function isInternalLink(link) {
    if (!link || !link.href) return false;
    if (link.target === "_blank") return false;
    if (link.hasAttribute("data-hs-ignore")) return false;
    if (link.hasAttribute("download")) return false;

    const url = new URL(link.href, window.location.origin);

    // Must be same origin
    if (url.origin !== window.location.origin) return false;

    // Must be HTML page (no extension or .html)
    const path = url.pathname;
    const ext = path.split(".").pop();
    if (ext && !["html", "htm"].includes(ext) && path.includes("."))
      return false;

    return true;
  }

  // ==================== Event Handlers ====================

  function initEventHandlers() {
    // Click handler
    document.addEventListener("click", (e) => {
      const link = e.target.closest("a");
      if (!isInternalLink(link)) return;
      if (e.ctrlKey || e.metaKey || e.shiftKey) return;

      e.preventDefault();

      const url = link.href;
      if (url === window.location.href) return;

      navigate(url);
    });

    // Hover handler for prefetch
    if (
      CONFIG.prefetch.enabled &&
      (CONFIG.prefetch.on === "hover" || CONFIG.prefetch.on === "both")
    ) {
      document.addEventListener("mouseover", (e) => {
        const link = e.target.closest("a");
        if (!isInternalLink(link)) return;

        clearTimeout(prefetchTimeout);
        prefetchTimeout = setTimeout(() => {
          prefetch(link.href);
        }, CONFIG.prefetch.delay);
      });

      document.addEventListener("mouseout", (e) => {
        if (e.target.closest("a")) {
          clearTimeout(prefetchTimeout);
        }
      });
    }

    // Touch handler for mobile prefetch
    document.addEventListener(
      "touchstart",
      (e) => {
        const link = e.target.closest("a");
        if (isInternalLink(link)) {
          prefetch(link.href);
        }
      },
      { passive: true },
    );

    // Back/forward navigation
    window.addEventListener("popstate", (e) => {
      if (e.state?.url) {
        navigate(e.state.url, false);
      } else {
        navigate(window.location.href, false);
      }
    });

    // Re-observe links after navigation (for dynamically added links)
    document.addEventListener("hs:afterNavigate", () => {
      if (visibleObserver) {
        observeLinks();
      }
    });
  }

  // ==================== Initialization ====================

  function init() {
    initProgressBar();
    initEventHandlers();
    initVisiblePrefetch();

    // Set initial state
    history.replaceState({ url: window.location.href }, document.title);

    console.log("[HotStatic] Router initialized", CONFIG);
  }

  // ==================== Public API ====================

  window.HotStatic = {
    // Navigate to URL
    navigate: (url) => navigate(url),

    // Prefetch a URL
    prefetch: (url) => prefetch(url),

    // Clear cache
    clearCache: () => cache.clear(),

    // Get cached URLs
    getCachedUrls: () => cache.keys(),

    // Get current config
    getConfig: () => ({ ...CONFIG }),

    // Version
    version: "2.0.0",
  };

  // Initialize when DOM is ready
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
