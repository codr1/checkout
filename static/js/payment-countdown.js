// Payment countdown timer - handles visual progress for both QR and Terminal payments
function initPaymentCountdown(countdownElementId, progressElementId, timeoutSeconds = 120) {
    let timeRemaining = timeoutSeconds;
    const totalTime = timeoutSeconds;
    const countdownElement = document.getElementById(countdownElementId);
    const progressElement = document.getElementById(progressElementId);
    
    function updateProgress() {
        if (timeRemaining <= 0) return;
        
        timeRemaining--;
        if (countdownElement) {
            countdownElement.textContent = timeRemaining;
        }
        
        if (progressElement) {
            const progressPercent = ((totalTime - timeRemaining) / totalTime) * 100;
            progressElement.style.width = progressPercent + '%';
        }
        
        if (timeRemaining > 0) {
            setTimeout(updateProgress, 1000);
        }
    }
    
    // Start countdown after a short delay
    setTimeout(updateProgress, 1000);
}

// Auto-initialization is now handled in templates with config-driven timeout values