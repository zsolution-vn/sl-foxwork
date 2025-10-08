// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

/**
 * BodyClassManager ensures that essential body classes like 'app__body' are always preserved
 * even when other components try to modify the body class attribute directly.
 */

// Essential classes that should never be removed from body
const ESSENTIAL_BODY_CLASSES = [
    "app__body",
    "app_body",
    "font--open_sans",
    "enable-animations",
];

// Store the original body classes from root.html
let originalBodyClasses: string[] = [];

/**
 * Initialize the body class manager with the original classes from root.html
 */
export function initializeBodyClassManager(): void {
    // Get the original classes from the body element
    originalBodyClasses = Array.from(document.body.classList);

    // Ensure essential classes are present
    ESSENTIAL_BODY_CLASSES.forEach((className) => {
        if (!document.body.classList.contains(className)) {
            document.body.classList.add(className);
        }
    });
}

/**
 * Safely add classes to body without removing essential classes
 */
export function addBodyClasses(...classes: string[]): void {
    classes.forEach((className) => {
        if (!document.body.classList.contains(className)) {
            document.body.classList.add(className);
        }
    });
}

/**
 * Safely remove classes from body while preserving essential classes
 */
export function removeBodyClasses(...classes: string[]): void {
    classes.forEach((className) => {
        // Only remove if it's not an essential class
        if (!ESSENTIAL_BODY_CLASSES.includes(className)) {
            document.body.classList.remove(className);
        }
    });
}

/**
 * Safely set body classes while preserving essential classes
 * This is a safer alternative to setAttribute('class', ...)
 */
export function setBodyClasses(...classes: string[]): void {
    // First, remove all non-essential classes
    const currentClasses = Array.from(document.body.classList);
    currentClasses.forEach((className) => {
        if (!ESSENTIAL_BODY_CLASSES.includes(className)) {
            document.body.classList.remove(className);
        }
    });

    // Then add the new classes
    addBodyClasses(...classes);
}

/**
 * Restore essential body classes if they were accidentally removed
 * This can be called periodically or after DOM mutations
 */
export function restoreEssentialBodyClasses(): void {
    ESSENTIAL_BODY_CLASSES.forEach((className) => {
        if (!document.body.classList.contains(className)) {
            document.body.classList.add(className);
        }
    });
}

/**
 * Get current body classes
 */
export function getBodyClasses(): string[] {
    return Array.from(document.body.classList);
}

/**
 * Check if a specific class exists on body
 */
export function hasBodyClass(className: string): boolean {
    return document.body.classList.contains(className);
}
