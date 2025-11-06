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
    const presetSelect = document.getElementById('simulation-presets');
    const resetButton = document.getElementById('reset-button');
    const directionSelect = document.getElementById('direction');
    const ifbWarning = document.getElementById('ifb-warning');

    const delayInput = document.getElementById('delay');
    const jitterInput = document.getElementById('jitter');
    const delayCorrelationInput = document.getElementById('delayCorrelation');
    const distributionInput = document.getElementById('distribution');
    const reorderInput = document.getElementById('reorder'); 
    const lossInput = document.getElementById('loss');
    const corruptInput = document.getElementById('corrupt');
    const duplicateInput = document.getElementById('duplicate');
    const lossCorrelationInput = document.getElementById('lossCorrelation');
    const corruptCorrelationInput = document.getElementById('corruptCorrelation');
    const duplicateCorrelationInput = document.getElementById('duplicateCorrelation');
    const reorderCorrelationInput = document.getElementById('reorderCorrelation');
    const reorderGapInput = document.getElementById('reorderGap'); 

    const lossModelSelect = document.getElementById('lossModel');
    const lossModelForms = {
        'none': null,
        'random': document.getElementById('loss-random-form'),
        'state': document.getElementById('loss-state-form'),
        'gemodel': document.getElementById('loss-gemodel-form')
    };

    const applyButton = document.getElementById('apply-button');
    // Inputs that must be filled to enable 'Apply' button
    const valueInputs = [
        document.getElementById('rate-value'),
        document.getElementById('delay'),
        // 'random'
        document.getElementById('loss'),
        // 'state'
        document.getElementById('lossStateP13'),
        // 'gemodel'
        document.getElementById('lossGemodelP'),
        // 'others'
        document.getElementById('corrupt'),
        document.getElementById('duplicate'),
        document.getElementById('reorder'),
        document.getElementById('reorderGap'),
    ];

    let selectedInterface = null; // Stores the selected interface

    const presets = {
        // --- 1. Mobile Networks ---
        '5g-ideal': {
            'rate-value': '100', 'rate-unit': 'mbit', 'delay': '20', 'jitter': '5', 'loss': ''
        },
        '4g-good': {
            'rate-value': '25', 'rate-unit': 'mbit', 'delay': '80', 'jitter': '15', 'loss': '0.1'
        },
        '4g-poor': {
            'rate-value': '5', 'rate-unit': 'mbit', 'delay': '150', 'jitter': '50', 'loss': '1'
        },
        '3g-legacy': {
            'rate-value': '1', 'rate-unit': 'mbit', 'delay': '400', 'jitter': '100', 'loss': '3'
        },
        // --- 2. Wi-Fi & WAN ---
        'nationwide-network': {
            'rate-value': '50', 'rate-unit': 'mbit', 'delay': '40', 'jitter': '10', 'loss': ''
        },
        'oversea-network': { // unlimited rate (no 'rate-value')
            'delay': '120', 'jitter': '10', 'loss': ''
        },
        'leo-satellite': {
            'rate-value': '15', 'rate-unit': 'mbit', 'delay': '80', 'jitter': '30', 'loss': '0.5'
        },
        'geo-satellite': {
            'rate-value': '3', 'rate-unit': 'mbit', 'delay': '600', 'jitter': '200', 'loss': '1'
        },
        'slow-stable-adsl': {
            'rate-value': '512', 'rate-unit': 'kbit', 'delay': '100', 'jitter': '20', 'loss': '0.1'
        },
        // --- 3. Problematic Networks ---
        'unstable-wifi': { // unlimited rate (no 'rate-value')
            'delay': '40', 'jitter': '20', 'loss': '2'
        },
        'unstable-voip': {
            'rate-value': '10', 'rate-unit': 'mbit', 'delay': '50', 'jitter': '150', 'loss': '1'
        },
        'bad-network': {
            'rate-value': '5', 'rate-unit': 'mbit', 'delay': '100', 'jitter': '50', 'loss': '8'
        }
    };

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
     * Generic helper to disable a child input if its parent is empty/zero
     * @param {HTMLElement} parentInput 
     * @param {HTMLElement} childInput 
     */
    function updateDependency(parentInput, childInput) {
        const hasParentValue = (parentInput.value !== "" && parseFloat(parentInput.value) > 0);
        childInput.disabled = !hasParentValue;
        if (!hasParentValue) {
            childInput.value = "";
        }
    }

    /**
     * Updates the disabled state of netem inputs based on dependencies
     */
    function updateInputDependencies() {
        const delayVal = delayInput.value;

        // Check if delay is set
        const hasDelay = (delayVal !== "" && delayVal !== "0");
        
        // Jitter, Correlation, Distribution and Reorder all depend on Delay
        jitterInput.disabled = !hasDelay;
        delayCorrelationInput.disabled = !hasDelay;
        distributionInput.disabled = !hasDelay;
        reorderInput.disabled = !hasDelay; // Reorder parent

        // If Delay is gone, clear them
        if (!hasDelay) {
            jitterInput.value = "";
            delayCorrelationInput.value = "";
            distributionInput.value = ""; // Or set to default
            reorderInput.value = "";
        }

        // Now, check Jitter
        const hasJitter = (jitterInput.value !== "" && parseFloat(jitterInput.value) > 0);
        
        // Delay Correlation depends on Jitter
        // (It's already disabled if delay is 0, but we add this for clarity)
        if (!hasJitter) {
            delayCorrelationInput.disabled = true;
            delayCorrelationInput.value = "";
        } else if (hasDelay) { // Only re-enable if parent (delay) is set
            delayCorrelationInput.disabled = false;
        }

        updateDependency(lossInput, lossCorrelationInput);
        updateDependency(corruptInput, corruptCorrelationInput);
        updateDependency(duplicateInput, duplicateCorrelationInput);
        updateDependency(reorderInput, reorderCorrelationInput); // Reorder child
        updateDependency(reorderCorrelationInput, reorderGapInput);

        updateApplyButtonState();
    }

    /**
     * Shows the correct form section for the selected loss model
     */
    function updateLossModelUI() {
        const selectedModel = lossModelSelect.value;
        
        // Hide all model forms
        Object.values(lossModelForms).forEach(form => {
            if (form) form.classList.add('hidden');
        });

        // Show the selected one
        if (lossModelForms[selectedModel]) {
            lossModelForms[selectedModel].classList.remove('hidden');
        }
    }

    /**
     * Checks if any simulation parameter is set and enables/disables the Apply button.
     */
    function updateApplyButtonState() {
        let hasValue = false;
        for (const input of valueInputs) {
            if (input.value !== "" && parseFloat(input.value) > 0) {
                hasValue = true;
                break;
            }
        }
        applyButton.disabled = !hasValue;
    }

    /**
     * Resets the form and applies a selected preset
     * @param {string} presetName - The key from the presets object
     */
    function applyPreset(presetName) {
        if (!presetName) {
            return; // User selected "(Select a preset...)"
        }

        const preset = presets[presetName];
        if (!preset) {
            logMessage(`Error: Preset "${presetName}" not found.`, 'error');
            return;
        }

        // 1. Reset the form to clear all fields
        configForm.reset();

        // 2. Apply all values from the preset object
        for (const [key, value] of Object.entries(preset)) {
            const el = document.getElementById(key); // Assumes ID matches key
            if (el) {
                el.value = value;
            }
        }

        // 3. Ensure "random" model is selected if a loss preset was applied
        let model = 'random'; // default
        if (preset.lossStateP13) {
            model = 'state';
        } else if (preset.lossGemodelP) {
            model = 'gemodel';
        }
        lossModelSelect.value = model;

        // 4. Update all UI dependencies
        updateInputDependencies();
        updateLossModelUI();
        updateApplyButtonState(); // This will enable the apply button
        
        logMessage(`Applied preset: ${presetName}`, 'success');
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
        const fields = [
            // Latency
            'delay', 'jitter', 'delayCorrelation', 'distribution',
            // Loss Model (selector)
            'lossModel',
            // Loss Random
            'loss', 'lossCorrelation',
            // Loss State
            'lossStateP13', 'lossStateP31', 'lossStateP32', 'lossStateP23', 'lossStateP14',
            // Loss Gemodel
            'lossGemodelP', 'lossGemodelR', 'lossGemodel1h', 'lossGemodel1k',
            // Other Manipulations
            'corrupt', 'corruptCorrelation',
            'duplicate', 'duplicateCorrelation',
            'reorder', 'reorderCorrelation', 'reorderGap',
        ];
        
        const rateVal = formData.get('rate-value');
        const rateUnit = formData.get('rate-unit');
        if (rateVal) {
            // Concat value and unit, eg: "100mbit"
            params.append('rate', rateVal + rateUnit);
        }

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
            updateInputDependencies(); 
            updateLossModelUI();       
            updateApplyButtonState();  
        } catch (err) {
            logMessage(`Failed to reset rules.`, 'error');
        }
    });

    delayInput.addEventListener('input', updateInputDependencies);
    jitterInput.addEventListener('input', updateInputDependencies);
    lossInput.addEventListener('input', updateInputDependencies);
    corruptInput.addEventListener('input', updateInputDependencies);
    duplicateInput.addEventListener('input', updateInputDependencies);
    reorderInput.addEventListener('input', updateInputDependencies);
    lossModelSelect.addEventListener('input', updateLossModelUI);

    presetSelect.addEventListener('input', (e) => applyPreset(e.target.value));

    valueInputs.forEach(input => {
        input.addEventListener('input', updateApplyButtonState);
    });

    // Initialize the application
    fetchInterfaces();
    updateInputDependencies(); // Call on load to set initial state
    updateLossModelUI();    
    updateApplyButtonState();
});