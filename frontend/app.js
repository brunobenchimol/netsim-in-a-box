// Wait for the DOM to be ready
document.addEventListener('DOMContentLoaded', () => {
    
    // DOM References
    const loadingEl = document.getElementById('loading-interfaces');
    const interfacesListEl = document.getElementById('interfaces-list');
    const configFormSection = document.getElementById('config-form-section');
    const selectedIfaceNameEl = document.getElementById('selected-iface-name');
    const logOutputEl = document.getElementById('log-output');

    // New form references
    const configForm = document.getElementById('config-form');
    const resetButton = document.getElementById('reset-button');
    const directionSelect = document.getElementById('direction');
    const ifbWarning = document.getElementById('ifb-warning');

    let selectedInterface = null; // Stores the selected interface

    /**
     * Writes a message to the UI log
     * @param {string} message - The message to log
     * @param {'info' | 'error' | 'success'} type - The message type
     */
    function logMessage(message, type = 'info') {
        const timestamp = new Date().toLocaleTimeString();
        let colorClass = 'text-gray-400'; // info
        if (type === 'error') {
            colorClass = 'text-red-400';
        } else if (type === 'success') {
            colorClass = 'text-green-400';
        }
        
        const logLine = document.createElement('div');
        logLine.innerHTML = `<span class="text-gray-500">${timestamp}:</span> <span class="${colorClass}">${message}</span>`;
        logOutputEl.appendChild(logLine);
        
        // Auto-scroll to the bottom
        logOutputEl.scrollTop = logOutputEl.scrollHeight;
    }

    /**
     * Helper function for making API calls
     * @param {string} endpoint - The API endpoint to call
     * @param {string} successMessage - Message to log on success
     */
    async function apiRequest(endpoint, successMessage) {
        logMessage(`Calling API: ${endpoint}`, 'info');
        try {
            const response = await fetch(endpoint);
            const responseText = await response.text(); // Read text first

            if (!response.ok) {
                // Try to parse error from JSON, fallback to text
                try {
                    const errJson = JSON.parse(responseText);
                    throw new Error(`API Error: ${errJson.message || 'Unknown error'}`);
                } catch (e) {
                    throw new Error(`API Error: ${response.status} ${response.statusText}. ${responseText}`);
                }
            }
            
            logMessage(successMessage, 'success');
            return responseText; // Can be JSON or empty string

        } catch (err) {
            logMessage(err.message, 'error');
            throw err; // Re-throw to calling function
        }
    }

    /**
     * Fetches network interfaces from the API
     */
    async function fetchInterfaces() {
        logMessage('Fetching network interfaces from API...');
        try {
            // The API is served on the same port (e.g., http://localhost:2023)
            const response = await fetch('/tc/api/v1/init');

            if (!response.ok) {
                const errorText = await response.text();
                throw new Error(`API Error: ${response.status} ${response.statusText}. ${errorText}`);
            }

            const data = await response.json();

            if (data.ifaces && data.ifaces.length > 0) {
                logMessage(`Successfully fetched ${data.ifaces.length} interfaces.`, 'success');
                loadingEl.style.display = 'none'; // Hide the loading spinner
                renderInterfaces(data.ifaces);
            } else {
                throw new Error('No network interfaces (ifaces) found in API response.');
            }

        } catch (err) {
            logMessage(err.message, 'error');
            loadingEl.textContent = 'Failed to load network interfaces. Check logs.';
            loadingEl.classList.add('text-red-400');
        }
    }

    /**
     * Renders the interface cards
     * @param {Array} ifaces - Array of interface objects
     */
    function renderInterfaces(ifaces) {
        interfacesListEl.innerHTML = ''; // Clear the list

        ifaces.forEach(iface => {
            const card = document.createElement('div');
            card.className = 'bg-gray-700 p-4 rounded-lg shadow-inner cursor-pointer hover:bg-blue-600 transition-colors';
            card.innerHTML = `
                <h3 class="text-lg font-bold text-white">${iface.name}</h3>
                <p class="text-sm text-gray-300">IPv4: ${iface.ipv4 || 'N/A'}</p>
                <p class="text-sm text-gray-300">IPv6: ${iface.ipv6 || 'N/A'}</p>
            `;
            
            // Add the click event listener
            card.addEventListener('click', () => selectInterface(iface, card));
            
            interfacesListEl.appendChild(card);
        });
    }

    /**
     * Handles selecting an interface
     * @param {object} iface - The selected interface object
     * @param {HTMLElement} selectedCard - The clicked card element
     */
    function selectInterface(iface, selectedCard) {
        logMessage(`Selected interface: ${iface.name}`);
        selectedInterface = iface;

        // Remove selection from other cards
        document.querySelectorAll('#interfaces-list > div').forEach(card => {
            card.classList.remove('bg-blue-700', 'ring-2', 'ring-blue-300');
            card.classList.add('bg-gray-700');
        });

        // Highlight the selected card
        selectedCard.classList.add('bg-blue-700', 'ring-2', 'ring-blue-300');
        selectedCard.classList.remove('bg-gray-700');

        // Show the configuration form
        selectedIfaceNameEl.textContent = iface.name;
        configFormSection.style.display = 'block';
    }

    /**
     * Shows or hides the IFB warning based on direction
     */
    directionSelect.addEventListener('change', (e) => {
        if (e.target.value === 'incoming') {
            ifbWarning.style.display = 'block';
        } else {
            ifbWarning.style.display = 'none';
        }
    });


    /**
     * Handles the form submission to apply TC rules
     */
    configForm.addEventListener('submit', async (e) => {
        e.preventDefault(); // Prevent default form POST
        
        if (!selectedInterface) {
            logMessage('Error: No interface selected.', 'error');
            return;
        }

        const formData = new FormData(configForm);
        const params = new URLSearchParams();

        // Add required parameters
        params.append('iface', selectedInterface.name);
        params.append('direction', formData.get('direction'));

        // --- Simplified Rules ---
        // We set defaults for rules not yet in the UI
        params.append('protocol', 'all');
        params.append('identifyKey', 'all');
        params.append('identifyValue', 'all');

        // Add rules from the form (if they have a value)
        let strategyCount = 1;
        
        const loss = formData.get('loss');
        if (loss) {
            params.append('strategy', 'loss');
            params.append('loss', loss);
            strategyCount++;
        }

        const delay = formData.get('delay');
        if (delay) {
            const strategyKey = (strategyCount === 1) ? 'strategy' : 'strategy2';
            const delayKey = (strategyCount === 1) ? 'delay' : 'delay2';
            params.append(strategyKey, 'delay');
            params.append(delayKey, delay);
            strategyCount++;
        }
        
        const rate = formData.get('rate');
        if (rate) {
            const strategyKey = (strategyCount === 1) ? 'strategy' : 'strategy2';
            const rateKey = (strategyCount === 1) ? 'rate' : 'rate2';
            params.append(strategyKey, 'rate');
            params.append(rateKey, rate);
        }

        // Use setup2 if multiple strategies, otherwise use setup
        const endpoint = (strategyCount > 2) ? '/tc/api/v1/config/setup2' : '/tc/api/v1/config/setup';
        
        try {
            await apiRequest(
                `${endpoint}?${params.toString()}`,
                `Successfully applied rules to ${selectedInterface.name}.`
            );
        } catch (err) {
            // Error is already logged by apiRequest
            logMessage(`Failed to apply rules.`, 'error');
        }
    });

    /**
     * Handles resetting all TC rules on the selected interface
     */
    resetButton.addEventListener('click', async () => {
        if (!selectedInterface) {
            logMessage('Error: No interface selected.', 'error');
            return;
        }

        const params = new URLSearchParams({ iface: selectedInterface.name });

        try {
            await apiRequest(
                `/tc/api/v1/config/reset?${params.toString()}`,
                `Successfully reset all rules on ${selectedInterface.name}.`
            );
            // Clear the form fields after a successful reset
            configForm.reset();
            ifbWarning.style.display = 'none';

        } catch (err) {
            // Error is already logged by apiRequest
            logMessage(`Failed to reset rules.`, 'error');
        }
    });

    // Initialize the application
    fetchInterfaces();
});