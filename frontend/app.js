// Wait for the DOM to be ready
document.addEventListener('DOMContentLoaded', () => {
    
    // API Version
    const API_VERSION = 'v2'; // The route path is still /v2/
    
    // DOM References
    const loadingEl = document.getElementById('loading-interfaces');
    const interfacesListEl = document.getElementById('interfaces-list');
    const configFormSection = document.getElementById('config-form-section');
    const selectedIfaceNameEl = document.getElementById('selected-iface-name');
    const logOutputEl = document.getElementById('log-output');

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
     * Helper for making API calls
     * @param {string} endpoint - The API endpoint
     * @param {string} successMessage - Message to log on success
     */
    async function apiRequest(endpoint, successMessage) {
        logMessage(`Calling API: ${endpoint}`, 'info');
        try {
            const response = await fetch(endpoint);
            const responseText = await response.text(); // Read text first

            if (!response.ok) {
                // Try to parse error from V4 JSON, fallback to text
                try {
                    const errJson = JSON.parse(responseText);
                    throw new Error(`API Error: ${errJson.message || 'Unknown error'}`);
                } catch (e) {
                    throw new Error(`API Error: ${response.status} ${response.statusText}. ${responseText}`);
                }
            }
            
            logMessage(successMessage, 'success');
            return responseText;

        } catch (err) {
            logMessage(err.message, 'error');
            throw err; // Re-throw to calling function
        }
    }

    /**
     * Fetches network interfaces from the API
     */
    async function fetchInterfaces() {
        logMessage(`Fetching network interfaces from API (/${API_VERSION}/config/init)...`);
        try {
            const response = await fetch(`/tc/api/${API_VERSION}/config/init`);

            if (!response.ok) {
                const errorText = await response.text();
                throw new Error(`API Error: ${response.status} ${response.statusText}. ${errorText}`);
            }

            const data = await response.json();

            if (data.ifaces && data.ifaces.length > 0) {
                logMessage(`Successfully fetched ${data.ifaces.length} interfaces.`, 'success');
                loadingEl.style.display = 'none';
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

        document.querySelectorAll('#interfaces-list > div').forEach(card => {
            card.classList.remove('bg-blue-700', 'ring-2', 'ring-blue-300');
            card.classList.add('bg-gray-700');
        });

        selectedCard.classList.add('bg-blue-700', 'ring-2', 'ring-blue-300');
        selectedCard.classList.remove('bg-gray-700');

        selectedIfaceNameEl.textContent = iface.name;
        configFormSection.style.display = 'block';
    }

    /**
     * Shows or hides the IFB warning
     */
    directionSelect.addEventListener('change', (e) => {
        if (e.target.value === 'incoming') {
            ifbWarning.style.display = 'block';
        } else {
            ifbWarning.style.display = 'none';
        }
    });


    /**
     * Handles the form submission to apply rules
     */
    configForm.addEventListener('submit', async (e) => {
        e.preventDefault();
        
        if (!selectedInterface) {
            logMessage('Error: No interface selected.', 'error');
            return;
        }

        const formData = new FormData(configForm);
        const params = new URLSearchParams();

        // 1. Required Parameters
        params.append('iface', selectedInterface.name);
        params.append('direction', formData.get('direction'));
        
        // 2. Add all other fields *only if they have a value*
        // V4: Added new correlation and distribution fields
        const fields = [
            'rate', 'delay', 'jitter', 'delayCorrelation', 'distribution',
            'loss', 'lossCorrelation', 'corrupt', 'duplicate', 'reorder'
        ];
        
        fields.forEach(field => {
            const value = formData.get(field);
            if (value) {
                params.append(field, value);
            }
        });
        
        // 4. Builds and calls the setup endpoint
        const endpoint = `/tc/api/${API_VERSION}/config/setup?${params.toString()}`;
        
        try {
            await apiRequest(
                endpoint,
                `Successfully applied V4 (native) rules to ${selectedInterface.name}.`
            );
        } catch (err) {
            logMessage(`Failed to apply V4 rules.`, 'error');
        }
    });

    /**
     * Handles resetting all rules
     */
    resetButton.addEventListener('click', async () => {
        if (!selectedInterface) {
            logMessage('Error: No interface selected.', 'error');
            return;
        }

        const params = new URLSearchParams({ iface: selectedInterface.name });
        const endpoint = `/tc/api/${API_VERSION}/config/reset?${params.toString()}`;

        try {
            await apiRequest(
                endpoint,
                `Successfully reset all rules on ${selectedInterface.name}.`
            );
            configForm.reset();
            ifbWarning.style.display = 'none';
        } catch (err) {
            logMessage(`Failed to reset rules.`, 'error');
        }
    });

    // Initialize the application
    fetchInterfaces();
});